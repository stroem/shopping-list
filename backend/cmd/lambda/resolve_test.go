package main

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

type fakeSSM struct {
	out *ssm.GetParameterOutput
	err error
	got string // captured parameter name
}

func (f *fakeSSM) GetParameter(_ context.Context, in *ssm.GetParameterInput, _ ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	if in.Name != nil {
		f.got = *in.Name
	}
	return f.out, f.err
}

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestResolvePrefersEnv(t *testing.T) {
	f := &fakeSSM{err: errors.New("should not be called")}
	got, err := resolveDatabaseURL(context.Background(),
		env(map[string]string{"DATABASE_URL": "postgres://from-env"}), f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "postgres://from-env" {
		t.Fatalf("got %q, want env value", got)
	}
	if f.got != "" {
		t.Fatalf("SSM should not have been called, but got name %q", f.got)
	}
}

func TestResolveFetchesFromSSM(t *testing.T) {
	f := &fakeSSM{out: &ssm.GetParameterOutput{
		Parameter: &types.Parameter{Value: aws.String("postgres://from-ssm")},
	}}
	got, err := resolveDatabaseURL(context.Background(),
		env(map[string]string{"DATABASE_URL_PARAM": "/shopping-list/database-url"}), f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "postgres://from-ssm" {
		t.Fatalf("got %q, want ssm value", got)
	}
	if f.got != "/shopping-list/database-url" {
		t.Fatalf("requested param %q", f.got)
	}
}

func TestResolveSSMErrorPropagates(t *testing.T) {
	f := &fakeSSM{err: errors.New("boom")}
	_, err := resolveDatabaseURL(context.Background(),
		env(map[string]string{"DATABASE_URL_PARAM": "/p"}), f)
	if err == nil {
		t.Fatal("expected error from SSM, got nil")
	}
}

func TestResolveMissingConfig(t *testing.T) {
	_, err := resolveDatabaseURL(context.Background(), env(map[string]string{}), &fakeSSM{})
	if err == nil {
		t.Fatal("expected error when neither DATABASE_URL nor DATABASE_URL_PARAM set")
	}
}
