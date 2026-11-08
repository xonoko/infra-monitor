package app_test

import (
	"net/url"
	"strconv"
)

type urlT = url.URL

func parseURL(s string) (*url.URL, error) { return url.Parse(s) }
func itoa(i int) string                   { return strconv.Itoa(i) }
