package main

import (
	"errors"
	"net/http"

	"github.com/Comcast/webpa-common/secure"

	"github.com/Comcast/webpa-common/secure/handler"
	"github.com/Comcast/webpa-common/secure/key"
	"github.com/SermoDigital/jose/jwt"
	"github.com/go-kit/kit/log"
	"github.com/spf13/viper"
)

const (
	JWT          = "jwtValidators"
	AuthHeader   = "AuthHeader"
	defaultKeyID = `current`
)

type JWTValidator struct {
	// JWTKeys is used to create the key.Resolver for JWT verification keys
	Keys key.ResolverFactory `json:"keys"`

	// Custom is an optional configuration section that defines
	// custom rules for validation over and above the standard RFC rules.
	Custom secure.JWTValidatorFactory `json:"custom"`
}

func NewAuthorizationHandler(v *viper.Viper, logger log.Logger) (func(http.Handler) http.Handler, error) {
	validators, err := buildValidators(v, logger)
	if err != nil {
		return nil, err
	}

	authorizationDecorator := handler.AuthorizationHandler{
		Logger:    logger,
		Validator: validators,
	}.Decorate

	return authorizationDecorator, nil
}

func buildValidators(v *viper.Viper, logger log.Logger) (secure.Validators, error) {
	if ok := (v.IsSet(JWT) && v.IsSet(AuthHeader)); !ok {
		return nil, errors.New("No validators within configuration file")
	}

	// if a JWTKeys section was supplied, configure a JWS validator
	// and append it to the chain of validators
	jwtValidators, _ := jwtFromConfigToValidators(v, logger)
	authValidators, _ := authHeaderFromConfigToValidators(v)

	switch {
	case jwtValidators == nil && authValidators != nil:
		return authValidators, nil
	case jwtValidators != nil && authValidators == nil:
		return jwtValidators, nil
	case jwtValidators != nil && authValidators != nil:
		return appendValidators(jwtValidators, authValidators), nil
	default:
		return nil, errors.New("Failed to get validator")
	}
}

func jwtFromConfigToValidators(v *viper.Viper, l log.Logger) (secure.Validators, error) {
	if !v.IsSet(JWT) {
		return nil, errors.New("No" + JWT + "in configuration")
	}

	var (
		jwtVals []JWTValidator
		vals    []secure.Validator
	)

	v.UnmarshalKey(JWT, &jwtVals)

	for _, validatorDescriptor := range jwtVals {
		var keyResolver key.Resolver
		keyResolver, err := validatorDescriptor.Keys.NewResolver()
		if err != nil {
			return nil, err
		}

		vals = append(
			vals,
			secure.JWSValidator{
				DefaultKeyId:  DEFAULT_KEY_ID,
				Resolver:      keyResolver,
				JWTValidators: []*jwt.Validator{validatorDescriptor.Custom.New()},
			},
		)
	}

	return vals, nil
}

func authHeaderFromConfigToValidators(v *viper.Viper) (secure.Validators, error) {
	if !v.IsSet(AuthHeader) {
		return nil, errors.New("No authHeader in configuration")
	}

	var vals []secure.Validator
	basicAuths := v.GetStringSlice(AuthHeader)
	for _, authValue := range basicAuths {
		vals = append(
			vals,
			secure.ExactMatchValidator(authValue),
		)
	}

	return vals, nil
}

// appendValidators
func appendValidators(jwtVals secure.Validators, authVals secure.Validators) secure.Validators {
	validators := jwtVals
	for _, v := range authVals {
		validators = append(validators, v)
	}

	return validators
}
