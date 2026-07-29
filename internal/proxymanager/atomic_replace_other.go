//go:build !windows

package proxymanager

import "os"

func atomicReplace(from, to string) error { return os.Rename(from, to) }
