package dreame

import (
	"crypto/aes"
	"crypto/md5"
	"encoding/hex"
	"strings"
)

const (
	UserAgent = "Dart/3.2 (dart:io)"
	BasicAuth = "Basic ZHJlYW1lX2FwcHYxOkFQXmR2QHpAU1FZVnhOODg="
	MetaCV    = "cv=i_829"
	Salt      = "RAylYC%fmSKp7%Tq"
	RLCKey    = "EETjszu*XI5znHsI"
	Lang      = "en"
)

// BaseURL is the regional Dreame gateway.
func BaseURL(region string) string {
	if region == "" {
		region = "us"
	}
	return "https://" + region + ".iot.dreame.tech:13267"
}

// PasswordHash is md5 hex of the plaintext password plus the vendor salt.
func PasswordHash(plain string) string {
	sum := md5.Sum([]byte(plain + Salt))
	return hex.EncodeToString(sum[:])
}

// PrefixFromBind returns the leading label of a bindDomain host.
func PrefixFromBind(bind string) string {
	host := bind
	if i := strings.IndexAny(host, ":/"); i >= 0 {
		host = host[:i]
	}
	if host == "" {
		return ""
	}
	return strings.Split(host, ".")[0]
}

// RLCHeader is the optional dreame-rlc value: hex AES-128-ECB of "{region}|{lang}|{country}".
func RLCHeader(region, lang, country string) (string, error) {
	if lang == "" {
		lang = Lang
	}
	plain := []byte(region + "|" + lang + "|" + country)
	block, err := aes.NewCipher([]byte(RLCKey))
	if err != nil {
		return "", err
	}
	padded := pkcs7(plain, block.BlockSize())
	out := make([]byte, len(padded))
	for i := 0; i < len(padded); i += block.BlockSize() {
		block.Encrypt(out[i:], padded[i:])
	}
	return hex.EncodeToString(out), nil
}

func pkcs7(b []byte, n int) []byte {
	pad := n - len(b)%n
	out := make([]byte, len(b)+pad)
	copy(out, b)
	for i := len(b); i < len(out); i++ {
		out[i] = byte(pad)
	}
	return out
}
