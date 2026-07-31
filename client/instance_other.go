//go:build !windows

package main

func acquireSingleInstance() (func(), bool) { return func() {}, true }
