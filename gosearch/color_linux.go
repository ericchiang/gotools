// +build linux

package main

func color(s string) string {
	return "\033[0;31m" + s + "\033[0m"
}
