package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

// paramGetter is the slice of the SSM API this command uses. The real
// *ssm.Client satisfies it; tests pass a fake.
type paramGetter interface {
	GetParameter(ctx context.Context, in *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
}

// resolveDatabaseURL returns the Postgres URL with precedence:
//  1. env DATABASE_URL (local/testing),
//  2. the SSM SecureString named by env DATABASE_URL_PARAM (decrypted).
// It errors if neither is configured.
func resolveDatabaseURL(ctx context.Context, env func(string) string, ssmc paramGetter) (string, error) {
	if url := env("DATABASE_URL"); url != "" {
		return url, nil
	}
	name := env("DATABASE_URL_PARAM")
	if name == "" {
		return "", errors.New("neither DATABASE_URL nor DATABASE_URL_PARAM is set")
	}
	out, err := ssmc.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           aws.String(name),
		WithDecryption: aws.Bool(true),
	})
	if err != nil {
		return "", fmt.Errorf("fetch %s from ssm: %w", name, err)
	}
	if out.Parameter == nil || out.Parameter.Value == nil {
		return "", fmt.Errorf("ssm parameter %s has no value", name)
	}
	return *out.Parameter.Value, nil
}
