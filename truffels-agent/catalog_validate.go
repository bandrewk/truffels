package main

import "regexp"

// catalogIDRe matches valid catalog entry IDs: lowercase letters, digits, hyphens,
// 1-32 characters long, must start with letter or digit.
var catalogIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// isValidCatalogID checks if the ID is a valid catalog entry identifier.
func isValidCatalogID(s string) bool { return catalogIDRe.MatchString(s) }
