//go:build !darwin || !cgo

package localauth

import "errors"

type StoredTokens struct {
	AccessToken  string
	RefreshToken string
	IDToken      string
}

var ErrKeychainUnsupported = errors.New("macOS Keychain support requires darwin with cgo")

func SaveTokens(StoredTokens) error {
	return ErrKeychainUnsupported
}

func LoadTokens() (StoredTokens, error) {
	return StoredTokens{}, ErrKeychainUnsupported
}

func ClearTokens() error {
	return ErrKeychainUnsupported
}
