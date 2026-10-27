package upstream

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
)

const (
	checkinRSAModulus  = "008aed7e057fe8f14c73550b0e6467b023616ddc8fa91846d2613cdb7f7621e3cada4cd5d812d627af6b87727ade4e26d26208b7326815941492b2204c3167ab2d53df1e3a2c9153bdb7c8c2e968df97a5e7e01cc410f92c4c2c2fba529b3ee988ebc1fca99ff5119e036d732c368acf8beba01aa2fdafa45b21e4de4928d0d403"
	checkinRSAExponent = "010001"
)

// EncryptPassword 复刻 UIAS 前端约定：密码先反转，再做原始 RSA 幂模运算。
func EncryptPassword(password string) (string, error) {
	if password == "" {
		return "", errors.New("密码不能为空")
	}
	return EncryptRaw(reverseString(password), checkinRSAModulus, checkinRSAExponent)
}

// EncryptRaw 用十六进制 modulus/exponent 做无填充 RSA，输出补齐到 256 位十六进制。
func EncryptRaw(plain, modulusHex, exponentHex string) (string, error) {
	if plain == "" {
		return "", errors.New("RSA 明文不能为空")
	}
	modulus, ok := new(big.Int).SetString(strings.TrimSpace(modulusHex), 16)
	if !ok || modulus.Sign() <= 0 {
		return "", errors.New("RSA modulus 无效")
	}
	exponent, ok := new(big.Int).SetString(strings.TrimSpace(exponentHex), 16)
	if !ok || exponent.Sign() <= 0 {
		return "", errors.New("RSA exponent 无效")
	}
	message := new(big.Int)
	for _, b := range asciiBytes(plain) {
		message.Lsh(message, 8)
		message.Or(message, big.NewInt(int64(b)))
	}
	encrypted := new(big.Int).Exp(message, exponent, modulus)
	encoded := encrypted.Text(16)
	if len(encoded) > 256 {
		return "", fmt.Errorf("RSA 结果长度异常: %d", len(encoded))
	}
	return strings.Repeat("0", 256-len(encoded)) + encoded, nil
}

func reverseString(s string) string {
	r := []rune(s)
	for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
		r[i], r[j] = r[j], r[i]
	}
	return string(r)
}

func asciiBytes(s string) []byte {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		if r > 127 {
			out = append(out, '?')
			continue
		}
		out = append(out, byte(r))
	}
	return out
}
