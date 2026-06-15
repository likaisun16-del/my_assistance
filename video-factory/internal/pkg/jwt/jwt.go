package jwt

import (
	"errors"
	"time"

	jwtv5 "github.com/golang-jwt/jwt/v5"
)

type JWT struct {
	secret []byte
	ttl    time.Duration
}

func New(secret string, ttlHours int) *JWT {
	return &JWT{secret: []byte(secret), ttl: time.Duration(ttlHours) * time.Hour}
}

type claims struct {
	UID int64 `json:"uid"`
	jwtv5.RegisteredClaims
}

func (j *JWT) Sign(uid int64) (string, error) {
	c := claims{
		UID: uid,
		RegisteredClaims: jwtv5.RegisteredClaims{
			ExpiresAt: jwtv5.NewNumericDate(time.Now().Add(j.ttl)),
			IssuedAt:  jwtv5.NewNumericDate(time.Now()),
		},
	}
	return jwtv5.NewWithClaims(jwtv5.SigningMethodHS256, c).SignedString(j.secret)
}

func (j *JWT) Parse(tok string) (int64, error) {
	parsed, err := jwtv5.ParseWithClaims(tok, &claims{}, func(t *jwtv5.Token) (any, error) {
		if _, ok := t.Method.(*jwtv5.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return j.secret, nil
	})
	if err != nil {
		return 0, err
	}
	c, ok := parsed.Claims.(*claims)
	if !ok || !parsed.Valid {
		return 0, errors.New("invalid token")
	}
	return c.UID, nil
}
