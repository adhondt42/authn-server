package config

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/url"
	"os"
	"strings"
	"time"

	_ "github.com/joho/godotenv/autoload"
	"golang.org/x/crypto/pbkdf2"
)

type Config struct {
	AppPasswordResetURL    *url.URL
	ApplicationDomains     []Domain
	BcryptCost             int
	UsernameIsEmail        bool
	UsernameMinLength      int
	UsernameDomains        []string
	PasswordMinComplexity  int
	RefreshTokenTTL        time.Duration
	RedisURL               *url.URL
	DatabaseURL            *url.URL
	SessionCookieName      string
	SessionSigningKey      []byte
	ResetSigningKey        []byte
	DBEncryptionKey        []byte
	ResetTokenTTL          time.Duration
	IdentitySigningKey     *rsa.PrivateKey
	AuthNURL               *url.URL
	ForceSSL               bool
	MountedPath            string
	AccessTokenTTL         time.Duration
	AuthUsername           string
	AuthPassword           string
	EnableSignup           bool
	StatisticsTimeZone     *time.Location
	DailyActivesRetention  int
	WeeklyActivesRetention int
}

var configurers = []configurer{
	// The APP_DOMAINS are a list of domains that may refer traffic and be valid JWT audiences. If
	// the domain includes a port, it must match referred traffic. If the domain does not include a
	// port, it will match any referred traffic port. Ports 80 and 443 are matched against schemes.
	func(c *Config) error {
		val, err := requireEnv("APP_DOMAINS")
		if err == nil {
			c.ApplicationDomains = make([]Domain, 0)
			for _, domain := range strings.Split(val, ",") {
				c.ApplicationDomains = append(c.ApplicationDomains, ParseDomain(domain))
			}
		}
		return err
	},

	// The AUTHN_URL is used as an issuer for ID tokens, and must be a URL that
	// the application can resolve in order to fetch our public key for JWT
	// verification.
	//
	// If the AUTHN_URL includes a path, all API routes will be relative to it.
	//
	// example: https://app.domain.com/authn
	func(c *Config) error {
		val, err := requireEnv("AUTHN_URL")
		if err == nil {
			authnURL, err := url.Parse(val)
			if err == nil {
				c.AuthNURL = authnURL
				c.MountedPath = authnURL.Path
				c.ForceSSL = authnURL.Scheme == "https"
			}
		}
		return err
	},

	// The SECRET_KEY_BASE is a random seed that AuthN can use to derive keys for
	// other purposes, like HMAC signing of JWT sessions with the AuthN server.
	// The key is not used directly, but is passed through an expensive derivation
	// that means any attempt to brute-force the base secret from a signature will
	// have a high work factor in addition to a large search space.
	//
	// This does not protect the derived key from being brute-forced, of course.
	// But it does help in case the key base has less entropy than might be ideal,
	// and it does protect from escalating an attack on one derived key into an
	// attack on all of the derived keys.
	func(c *Config) error {
		val, err := requireEnv("SECRET_KEY_BASE")
		if err == nil {
			c.SessionSigningKey = derive([]byte(val), "session-key-salt")
			c.ResetSigningKey = derive([]byte(val), "password-reset-token-key-salt")
			c.DBEncryptionKey = derive([]byte(val), "db-encryption-key-salt")[:32]
		}
		return err
	},

	// BCRYPT_COST describes how many times a password should be hashed. Costs are
	// exponential, and may be increased later without waiting for a user to return
	// and log in.
	//
	// The ideal cost is the slowest one that can be performed without a slow login
	// experience and without creating CPU bottlenecks or easy DDOS attack vectors.
	//
	// There's no reason to go below 10, and 12 starts to become noticeable on
	// current hardware.
	func(c *Config) error {
		cost, err := lookupInt("BCRYPT_COST", 11)
		if err == nil {
			if cost < 10 {
				return fmt.Errorf("BCRYPT_COST is too low: %v", cost)
			}
			c.BcryptCost = cost
		}
		return err
	},

	// PASSWORD_POLICY_SCORE is a minimum complexity score that a password must get
	// from the zxcvbn algorithm, where:
	//
	// * 0 - too guessable
	// * 1 - very guessable
	// * 2 - somewhat guessable (default)
	// * 3 - safely unguessable
	// * 4 - very unguessable
	//
	// See: see: https://blogs.dropbox.com/tech/2012/04/zxcvbn-realistic-password-strength-estimation/
	func(c *Config) error {
		minScore, err := lookupInt("PASSWORD_POLICY_SCORE", 2)
		if err == nil {
			c.PasswordMinComplexity = minScore
		}
		return err
	},

	// A DATABASE_URL is a string that can specify the database engine, connection
	// details, credentials, and other details.
	//
	// Example: sqlite3://localhost/authn-go
	func(c *Config) error {
		dbURL, err := requireEnv("DATABASE_URL")
		if err == nil {
			url, err := url.Parse(dbURL)
			if err == nil {
				c.DatabaseURL = url
			}
		}
		return err
	},

	// REDIS_URL is a string format that can specify any option for connecting to
	// a Redis server.
	//
	// Example: redis://127.0.0.1:6379/11
	func(c *Config) error {
		redisURL, err := requireEnv("REDIS_URL")
		if err == nil {
			url, err := url.Parse(redisURL)
			if err == nil {
				c.RedisURL = url
			}
		}
		return err
	},

	// USERNAME_IS_EMAIL is a truthy string ("t", "true", "yes") that enables the
	// email validations for username fields. By default, usernames are just
	// strings.
	func(c *Config) error {
		isEmail, err := lookupBool("USERNAME_IS_EMAIL", false)
		if err == nil {
			c.UsernameIsEmail = isEmail
		}
		return err
	},

	// ENABLE_SIGNUP may be set to a falsy value ("f", "false", "no") to disable
	// signup endpoints.
	func(c *Config) error {
		enableSignup, err := lookupBool("ENABLE_SIGNUP", true)
		if err == nil {
			c.EnableSignup = enableSignup
		}
		return err
	},

	// EMAIL_USERNAME_DOMAINS is a comma-delimited list of domains that an email
	// username must contain for signup. If missing, then any domain is a valid
	// signup.
	//
	// This setting only has effect if USERNAME_IS_EMAIL has been set.
	func(c *Config) error {
		if val, ok := os.LookupEnv("EMAIL_USERNAME_DOMAINS"); ok {
			c.UsernameDomains = strings.Split(val, ",")
		}
		return nil
	},

	// REFRESH_TOKEN_TTL determines how long a refresh token will live after its
	// last touch. This is necessary to prevent years-long Redis bloat from
	// inactive sessions, where users close the window rather than log out.
	func(c *Config) error {
		ttl, err := lookupInt("REFRESH_TOKEN_TTL", 86400*365.25)
		if err == nil {
			c.RefreshTokenTTL = time.Duration(ttl) * time.Second
		}
		return err
	},

	// PASSWORD_RESET_TOKEN_TTL determines how long a password reset token (as JWT)
	// will be valid from when it is generated. These tokens should not live much
	// longer than it takes for an attentive user to act in a reasonably expedient
	// manner. If a user loses control of a password reset token, they will lose
	// control of their account.
	func(c *Config) error {
		ttl, err := lookupInt("PASSWORD_RESET_TOKEN_TTL", 1800)
		if err == nil {
			c.ResetTokenTTL = time.Duration(ttl) * time.Second
		}
		return err
	},

	// ACCESS_TOKEN_TTL determines how long an access token (as JWT) will remain
	// valid. This is a hard limit, to limit the potential damage of an exposed
	// access token.
	//
	// New access tokens can be generated using the refresh token for as long as
	// the refresh token remains valid. This is an important mechanism because it
	// allows the authentication server to revoke sessions (e.g. on logout) with
	// confidence that any related access tokens will expire soon.
	//
	// Note that revoking a refresh token will not invalidate access tokens until
	// this TTL passes, so shorter is better.
	func(c *Config) error {
		ttl, err := lookupInt("ACCESS_TOKEN_TTL", 3600)
		if err == nil {
			c.AccessTokenTTL = time.Duration(ttl) * time.Second
		}
		return err
	},

	// HTTP_AUTH_USERNAME and HTTP_AUTH_PASSWORD specify the basic auth credentials
	// that must be provided to access private endpoints.
	//
	// This security pattern requires communication with AuthN to use SSL.
	func(c *Config) error {
		if val, ok := os.LookupEnv("HTTP_AUTH_USERNAME"); ok {
			c.AuthUsername = val
		} else {
			i, err := rand.Int(rand.Reader, big.NewInt(99999999))
			if err != nil {
				return err
			}
			c.AuthUsername = i.String()
		}
		if val, ok := os.LookupEnv("HTTP_AUTH_PASSWORD"); ok {
			c.AuthPassword = val
		} else {
			i, err := rand.Int(rand.Reader, big.NewInt(99999999))
			if err != nil {
				return err
			}
			c.AuthPassword = i.String()
		}
		return nil
	},

	// APP_PASSWORD_RESET_URL is an endpoint that will be notified when an account
	// has requested a password reset. The endpoint is expected to deliver an email
	// with the given password reset token, then respond with a 2xx HTTP status.
	//
	// For security, this URL should specify https and include a basic auth
	// username and password.
	func(c *Config) error {
		if val, ok := os.LookupEnv("APP_PASSWORD_RESET_URL"); ok {
			resetURL, err := url.Parse(val)
			if err == nil {
				c.AppPasswordResetURL = resetURL
			}
			return err
		}
		return nil
	},

	// RSA_PRIVATE_KEY is a RSA private key in PEM format. If provided as a single
	// line string, any literal \n sequences will be converted to real linebreaks.
	// When provided, it will be used for signing identity tokens, and the public
	// key will be published for audiences to verify. When not provided, AuthN will
	// generate and manage keys itself, using Redis for coordination and
	// persistence.
	func(c *Config) error {
		if str, ok := os.LookupEnv("RSA_PRIVATE_KEY"); ok {
			str = strings.Replace(str, `\n`, "\n", -1)
			block, _ := pem.Decode([]byte(str))
			key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
			if err != nil {
				return err
			}
			c.IdentitySigningKey = key
		}
		return nil
	},

	// TIME_ZONE is the IANA name of a location that should be used when calculating
	// which day it is when tracking key stats. It defaults to UTC.
	func(c *Config) error {
		name, ok := os.LookupEnv("TIME_ZONE")
		if !ok {
			name = "UTC"
		}

		tz, err := time.LoadLocation(name)
		if err != nil {
			return err
		}
		c.StatisticsTimeZone = tz
		return nil
	},

	// DAILY_ACTIVES_RETENTION is how many daily records of the number of active accounts to keep.
	// The default is 365 (~1 year).
	func(c *Config) error {
		num, err := lookupInt("DAILY_ACTIVES_RETENTION", 365)
		if err == nil {
			c.DailyActivesRetention = num
		}
		return err
	},

	// WEEKLY_ACTIVES_RETENTION is how many weekly records of the number of active accounts to keep.
	// The default is 104 (~2 years).
	func(c *Config) error {
		num, err := lookupInt("WEEKLY_ACTIVES_RETENTION", 104)
		if err == nil {
			c.WeeklyActivesRetention = num
		}
		return err
	},

	func(c *Config) error {
		c.UsernameMinLength = 3
		return nil
	},

	func(c *Config) error {
		c.SessionCookieName = "authn"
		return nil
	},
}

func ReadEnv() *Config {
	config, err := configure(configurers)
	if err != nil {
		panic(err)
	}

	return config
}

// 20k iterations of PBKDF2 HMAC SHA-256
func derive(base []byte, salt string) []byte {
	return pbkdf2.Key(base, []byte(salt), 2e4, 64, sha256.New)
}
