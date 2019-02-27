package main

type constantSecret struct {
	secret string
}

func (c *constantSecret) GetSecret() (string, error) {
	return c.secret, nil
}

func NewConstantSecret(secret string) *constantSecret {
	return &constantSecret{
		secret: secret,
	}
}
