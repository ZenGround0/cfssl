// Package config contains the configuration logic for CF-SSL.
package config

import (
	"crypto/x509"
	"encoding/json"
	"io/ioutil"
	"time"

	"github.com/cloudflare/cfssl/api/client"
	"github.com/cloudflare/cfssl/auth"
	"github.com/cloudflare/cfssl/helpers"
	"github.com/cloudflare/cfssl/log"
)

// A SigningProfile stores information that the CA needs to store
// signature policy.
type SigningProfile struct {
	Usage        []string `json:"usages"`
	IssuerURL    []string `json:"issuer_urls"`
	OCSP         string   `json:"ocsp_url"`
	CRL          string   `json:"crl_url"`
	CA           bool     `json:"is_ca"`
	ExpiryString string   `json:"expiry"`
	AuthKeyName  string   `json:"auth_key"`
	RemoteName   string   `json:"remote"`

	Expiry   time.Duration
	Provider auth.Provider
	Remote   *client.Server
}

// populate is used to fill in the fields that are not in JSON
//
// First, the ExpiryString parameter is needed to parse
// expiration timestamps from JSON. The JSON decoder is not able to
// decode a string time duration to a time.Duration, so this is called
// when loading the configuration to properly parse and fill out the
// Expiry parameter.
// This function is also used to create references to the auth key
// and default remote for the profile.
// It returns true if ExpiryString is a valid representation of a
// time.Duration, and the AuthKeyString and hkoteName point to
// valid objects. It returns false otherwise.
func (p *SigningProfile) populate(cfg *Config) bool {
	log.Debugf("parse expiry in profile")
	if p == nil {
		log.Debugf("failed: no timestamp in profile")
		return false
	} else if p.ExpiryString == "" {
		log.Debugf("failed: empty expiry string")
		return false
	}

	dur, err := time.ParseDuration(p.ExpiryString)
	if err != nil {
		log.Debugf("failed to parse expiry: %v", err)
		return false
	}

	log.Debugf("expiry is valid")
	p.Expiry = dur

	if p.AuthKeyName != "" {
		if key, ok := cfg.AuthKeys[p.AuthKeyName]; ok == true {
			if key.Type == "standard" {
				p.Provider, err = auth.New(key.Key, nil)
				if err != nil {
					log.Debugf("failed to create new stanard auth provider: %v", err)
					return false
				}
			} else {
				log.Debugf("unknown authentication type %v", key.Type)
				return false
			}
		} else {
			log.Debugf("failed to find auth_key %v in auth_keys section", p.AuthKeyName)
			return false
		}
	}

	if p.RemoteName != "" {
		if remote := cfg.Remotes[p.RemoteName]; remote != "" {
			p.Remote = client.NewServer(remote)
			if p.Remote == nil {
				log.Debugf("failed to connect to remote %v", remote)
				return false
			}
		} else {
			log.Debugf("failed to find remote %v in remotes section %v", p.RemoteName, cfg)
			return false
		}
	}

	return true
}

// Usages parses the list of key uses in the profile, translating them
// to a list of X.509 key usages and extended key usages.  The unknown
// uses are collected into a slice that is also returned.
func (p *SigningProfile) Usages() (ku x509.KeyUsage, eku []x509.ExtKeyUsage, unk []string) {
	for _, keyUse := range p.Usage {
		if kuse, ok := KeyUsage[keyUse]; ok {
			ku |= kuse
		} else if ekuse, ok := ExtKeyUsage[keyUse]; ok {
			eku = append(eku, ekuse)
		} else {
			unk = append(unk, keyUse)
		}
	}
	return
}

// A valid profile has defined at least key usages to be used, and a
// valid default profile has defined at least a default expiration.
func (p *SigningProfile) validProfile(isDefault bool) bool {
	log.Debugf("validate profile")
	if !isDefault {
		if len(p.Usage) == 0 {
			log.Debugf("invalid profile: no usages specified")
			return false
		} else if _, _, unk := p.Usages(); len(unk) == len(p.Usage) {
			log.Debugf("invalid profile: no valid usages")
			return false
		}
	} else {
		if p.Expiry == 0 {
			log.Debugf("invalid profile: no expiry set")
			return false
		}
	}
	log.Debugf("profile is valid")
	return true
}

// Signing codifies the signature configuration policy for a CA.
type Signing struct {
	Profiles map[string]*SigningProfile `json:"profiles"`
	Default  *SigningProfile            `json:"default"`
}

// Config stores configuration information for the CA.
type Config struct {
	Signing  *Signing           `json:"signing"`
	AuthKeys map[string]AuthKey `json:"auth_keys,omitempty"`
	Remotes  map[string]string  `json:"remotes,omitempty"`
}

// Valid ensures that Config is a valid configuration. It should be
// called immediately after parsing a configuration file.
func (c *Config) Valid() bool {
	return c.Signing.Valid()
}

// Valid checks the signature policies, ensuring they are valid
// policies. A policy is valid if it has defined at least key usages
// to be used, and a valid default profile has defined at least a
// default expiration.
func (s *Signing) Valid() bool {
	log.Debugf("validating configuration")
	if !s.Default.validProfile(true) {
		log.Debugf("default profile is invalid")
		return false
	}

	for _, p := range s.Profiles {
		if !p.validProfile(false) {
			log.Debugf("invalid profile")
			return false
		}
	}
	return true
}

// KeyUsage contains a mapping of string names to key usages.
var KeyUsage = map[string]x509.KeyUsage{
	"signing":             x509.KeyUsageDigitalSignature,
	"digital signature":   x509.KeyUsageDigitalSignature,
	"content committment": x509.KeyUsageContentCommitment,
	"key encipherment":    x509.KeyUsageKeyEncipherment,
	"data encipherment":   x509.KeyUsageDataEncipherment,
	"cert sign":           x509.KeyUsageCertSign,
	"crl sign":            x509.KeyUsageCRLSign,
	"encipher only":       x509.KeyUsageEncipherOnly,
	"decipher only":       x509.KeyUsageDecipherOnly,
}

// ExtKeyUsage contains a mapping of string names to extended key
// usages.
var ExtKeyUsage = map[string]x509.ExtKeyUsage{
	"any":              x509.ExtKeyUsageAny,
	"server auth":      x509.ExtKeyUsageServerAuth,
	"client auth":      x509.ExtKeyUsageClientAuth,
	"code signing":     x509.ExtKeyUsageCodeSigning,
	"email protection": x509.ExtKeyUsageEmailProtection,
	"s/mime":           x509.ExtKeyUsageEmailProtection,
	"ipsec end system": x509.ExtKeyUsageIPSECEndSystem,
	"ipsec tunnel":     x509.ExtKeyUsageIPSECTunnel,
	"ipsec user":       x509.ExtKeyUsageIPSECUser,
	"timestamping":     x509.ExtKeyUsageTimeStamping,
	"ocsp signing":     x509.ExtKeyUsageOCSPSigning,
	"microsoft sgc":    x509.ExtKeyUsageMicrosoftServerGatedCrypto,
	"netscape sgc":     x509.ExtKeyUsageNetscapeServerGatedCrypto,
}

// An AuthKey contains an entry for a key used for authentication.
type AuthKey struct {
	// Type contains information needed to select the appropriate
	// constructor. For example, "standard" for HMAC-SHA-256,
	// "standard-ip" for HMAC-SHA-256 incorporating the client's
	// IP.
	Type string `json:"type"`
	// Key contains the key information, such as a hex-encoded
	// HMAC key.
	Key string `json:"key"`
}

// DefaultConfig returns a default configuration specifying basic key
// usage and a 1 year expiration time. The key usages chosen are
// signing, key encipherment, client auth and server auth.
func DefaultConfig() *SigningProfile {
	d := helpers.OneYear
	return &SigningProfile{
		Usage:        []string{"signing", "key encipherment", "server auth", "client auth"},
		Expiry:       d,
		ExpiryString: "8760h",
	}
}

// LoadFile attempts to load the configuration file stored at the path
// and returns the configuration. On error, it returns nil.
func LoadFile(path string) *Config {
	log.Debugf("loading configuration file from %s", path)
	if path == "" {
		return nil
	}

	body, err := ioutil.ReadFile(path)
	if err != nil {
		log.Debugf("failed to read configuration file: %v", err)
		return nil
	}

	var cfg = &Config{}
	err = json.Unmarshal(body, &cfg)
	if err != nil {
		log.Debugf("failed to unmarshal configuration: %v", err)
		return nil
	}

	if cfg.Signing.Default == nil {
		log.Debugf("no default given: using default config")
		cfg.Signing.Default = DefaultConfig()
	} else {
		if !cfg.Signing.Default.populate(cfg) {
			return nil
		}
	}

	if !cfg.Valid() {
		return nil
	}

	for k := range cfg.Signing.Profiles {
		if !cfg.Signing.Profiles[k].populate(cfg) {
			return nil
		}
	}

	log.Debugf("configuration ok")
	return cfg
}
