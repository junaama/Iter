package localauth

import (
	"net/url"
	"os"
	"strings"
)

const (
	KeychainService        = "dev.iter.IterApp"
	KeychainAccessAccount  = "access_token"
	KeychainRefreshAccount = "refresh_token"
	KeychainIDAccount      = "id_token"
)

type Config struct {
	ClientID               string
	DeviceAuthorizationURL string
	TokenURL               string
}

func ConfigFromEnv() Config {
	base := firstNonEmpty(os.Getenv("ITER_WORKOS_BASE_URL"), "https://api.workos.com")
	return Config{
		ClientID: firstNonEmpty(os.Getenv("ITER_WORKOS_CLIENT_ID"), os.Getenv("WORKOS_CLIENT_ID")),
		DeviceAuthorizationURL: firstNonEmpty(
			os.Getenv("ITER_WORKOS_DEVICE_AUTH_URL"),
			joinURL(base, "/user_management/authorize/device"),
		),
		TokenURL: firstNonEmpty(
			os.Getenv("ITER_WORKOS_TOKEN_URL"),
			joinURL(base, "/user_management/authenticate"),
		),
	}
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.ClientID) == "" {
		return ErrMissingClientID
	}
	if _, err := url.ParseRequestURI(c.DeviceAuthorizationURL); err != nil {
		return err
	}
	if _, err := url.ParseRequestURI(c.TokenURL); err != nil {
		return err
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func joinURL(base, path string) string {
	u, err := url.Parse(base)
	if err != nil {
		return base + path
	}
	u.Path = strings.TrimRight(u.Path, "/") + path
	return u.String()
}
