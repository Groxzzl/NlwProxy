//go:build windows

package cli

import "golang.org/x/sys/windows/registry"

type registryCredentialSource struct{}

func (registryCredentialSource) Lookup(name string) (string, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE)
	if err != nil {
		return "", err
	}
	defer key.Close()
	value, _, err := key.GetStringValue(name)
	return value, err
}
