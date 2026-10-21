//go:build !linux

package idmap

import "errors"

func swap(pid int, hostPath, ctrPath string, replace bool) error {
	return errors.New("idmap repair requires Linux")
}
