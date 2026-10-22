package apikey

import (
	"strconv"
)

func parseCanonicalUnix(value string) (int64, error) {
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || strconv.FormatInt(parsed, 10) != value {
		return 0, strconv.ErrSyntax
	}
	return parsed, nil
}
