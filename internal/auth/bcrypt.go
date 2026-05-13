package auth

import "golang.org/x/crypto/bcrypt"

func bcryptCompare(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}
