package captcha

import (
	"errors"
	"fmt"
	"strings"

	gocaptcha "github.com/YN1753/GoCaptcha/captcha"
)

const CodeLength = 4

var ErrInvalidCode = errors.New("验证码不是 4 位 ASCII 字母或数字")

type Recognizer interface {
	Recognize(image []byte) (string, error)
}

type GoCaptcha struct {
	r *gocaptcha.Recognizer
}

func New() (*GoCaptcha, error) {
	r, err := gocaptcha.New()
	if err != nil {
		return nil, fmt.Errorf("初始化 GoCaptcha: %w", err)
	}
	return &GoCaptcha{r: r}, nil
}

func (g *GoCaptcha) Recognize(image []byte) (string, error) {
	if g == nil || g.r == nil {
		return "", errors.New("验证码识别器未初始化")
	}
	text, err := g.r.Recognize(image)
	if err != nil {
		return "", err
	}
	return Validate(text)
}

func (g *GoCaptcha) Close() error {
	if g == nil || g.r == nil {
		return nil
	}
	return g.r.Close()
}

func Validate(text string) (string, error) {
	code := strings.TrimSpace(text)
	runes := []rune(code)
	if len(runes) != CodeLength {
		return code, fmt.Errorf("%w: %q", ErrInvalidCode, code)
	}
	for _, r := range runes {
		if !isASCIIDigit(r) && !isASCIILetter(r) {
			return code, fmt.Errorf("%w: %q", ErrInvalidCode, code)
		}
	}
	return code, nil
}

func isASCIIDigit(r rune) bool  { return r >= '0' && r <= '9' }
func isASCIILetter(r rune) bool { return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') }
