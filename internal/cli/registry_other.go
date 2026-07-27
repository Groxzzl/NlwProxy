//go:build !windows

package cli

type registryCredentialSource struct{}

func (registryCredentialSource) Lookup(string) (string, error) { return "", nil }
