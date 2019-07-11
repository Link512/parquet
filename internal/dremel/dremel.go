package dremel

// Package dremel generates code that parquetgen
// uses to encode/decode a struct for writing and
// reading parquet files.

import (
	"github.com/parsyl/parquet/internal/parse"
)

func Write(i int, f parse.Field, fields []parse.Field) string {
	if !isOptional(f) && !isRepeated(f) {
		return writeRequired(f)
	}
	if isOptional(f) && !isRepeated(f) {
		return writeOptional(f)
	}

	return writeRepeated(i, f, fields)
}

func Read(f parse.Field) string {
	if isOptional(f) && !isRepeated(f) {
		return readOptional(f)
	}

	if isRepeated(f) {
		return readRepeated(f)
	}

	return readRequired(f)
}

func isRepeated(f parse.Field) bool {
	for _, o := range f.RepetitionTypes {
		if o == parse.Repeated {
			return true
		}
	}
	return false
}

func isOptional(f parse.Field) bool {
	for _, o := range f.RepetitionTypes {
		if o == parse.Optional {
			return true
		}
	}
	return false
}
