//go:build darwin && cgo

package localauth

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>

static CFStringRef iterCFString(const char *s, int len) {
	return CFStringCreateWithBytes(kCFAllocatorDefault, (const UInt8 *)s, len, kCFStringEncodingUTF8, false);
}

static CFDataRef iterCFData(const void *bytes, int len) {
	return CFDataCreate(kCFAllocatorDefault, (const UInt8 *)bytes, len);
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

type StoredTokens struct {
	AccessToken  string
	RefreshToken string
	IDToken      string
}

func SaveTokens(tokens StoredTokens) error {
	if err := setPassword(KeychainAccessAccount, tokens.AccessToken, false); err != nil {
		return err
	}
	if err := setPassword(KeychainRefreshAccount, tokens.RefreshToken, true); err != nil {
		return err
	}
	if tokens.IDToken == "" {
		return deletePassword(KeychainIDAccount)
	}
	return setPassword(KeychainIDAccount, tokens.IDToken, false)
}

func LoadTokens() (StoredTokens, error) {
	access, err := getPassword(KeychainAccessAccount)
	if err != nil {
		return StoredTokens{}, err
	}
	refresh, err := getPassword(KeychainRefreshAccount)
	if err != nil {
		return StoredTokens{}, err
	}
	idToken, err := getPassword(KeychainIDAccount)
	if err != nil && !isNotFound(err) {
		return StoredTokens{}, err
	}
	return StoredTokens{AccessToken: access, RefreshToken: refresh, IDToken: idToken}, nil
}

func ClearTokens() error {
	var firstErr error
	for _, account := range []string{KeychainAccessAccount, KeychainRefreshAccount, KeychainIDAccount} {
		if err := deletePassword(account); err != nil && !isNotFound(err) && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func setPassword(account, password string, deviceOnly bool) error {
	query := baseQuery(account)
	defer C.CFRelease(C.CFTypeRef(query))
	C.SecItemDelete(C.CFDictionaryRef(query))

	dataBytes := []byte(password)
	data := C.iterCFData(unsafe.Pointer(&dataBytes[0]), C.int(len(dataBytes)))
	defer C.CFRelease(C.CFTypeRef(data))
	C.CFDictionarySetValue(query, unsafe.Pointer(C.kSecValueData), unsafe.Pointer(data))
	if deviceOnly {
		C.CFDictionarySetValue(query, unsafe.Pointer(C.kSecAttrAccessible), unsafe.Pointer(C.kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly))
	} else {
		C.CFDictionarySetValue(query, unsafe.Pointer(C.kSecAttrAccessible), unsafe.Pointer(C.kSecAttrAccessibleAfterFirstUnlock))
	}
	status := C.SecItemAdd(C.CFDictionaryRef(query), nil)
	if status != C.errSecSuccess {
		return keychainError(status)
	}
	return nil
}

func getPassword(account string) (string, error) {
	query := baseQuery(account)
	defer C.CFRelease(C.CFTypeRef(query))
	C.CFDictionarySetValue(query, unsafe.Pointer(C.kSecReturnData), unsafe.Pointer(C.kCFBooleanTrue))
	C.CFDictionarySetValue(query, unsafe.Pointer(C.kSecMatchLimit), unsafe.Pointer(C.kSecMatchLimitOne))

	var result C.CFTypeRef
	status := C.SecItemCopyMatching(C.CFDictionaryRef(query), &result)
	if status != C.errSecSuccess {
		return "", keychainError(status)
	}
	defer C.CFRelease(result)
	data := C.CFDataRef(result)
	length := C.CFDataGetLength(data)
	bytes := C.CFDataGetBytePtr(data)
	return string(C.GoBytes(unsafe.Pointer(bytes), C.int(length))), nil
}

func deletePassword(account string) error {
	query := baseQuery(account)
	defer C.CFRelease(C.CFTypeRef(query))
	status := C.SecItemDelete(C.CFDictionaryRef(query))
	if status != C.errSecSuccess && status != C.errSecItemNotFound {
		return keychainError(status)
	}
	return nil
}

func baseQuery(account string) C.CFMutableDictionaryRef {
	dict := C.CFDictionaryCreateMutable(
		C.kCFAllocatorDefault,
		0,
		&C.kCFTypeDictionaryKeyCallBacks,
		&C.kCFTypeDictionaryValueCallBacks,
	)
	service := cfString(KeychainService)
	accountValue := cfString(account)
	C.CFDictionarySetValue(dict, unsafe.Pointer(C.kSecClass), unsafe.Pointer(C.kSecClassGenericPassword))
	C.CFDictionarySetValue(dict, unsafe.Pointer(C.kSecAttrService), unsafe.Pointer(service))
	C.CFDictionarySetValue(dict, unsafe.Pointer(C.kSecAttrAccount), unsafe.Pointer(accountValue))
	C.CFRelease(C.CFTypeRef(service))
	C.CFRelease(C.CFTypeRef(accountValue))
	return dict
}

func cfString(value string) C.CFStringRef {
	bytes := []byte(value)
	return C.iterCFString((*C.char)(unsafe.Pointer(&bytes[0])), C.int(len(bytes)))
}

func keychainError(status C.OSStatus) error {
	return fmt.Errorf("keychain status %d", int32(status))
}

func isNotFound(err error) bool {
	return err != nil && err.Error() == keychainError(C.errSecItemNotFound).Error()
}
