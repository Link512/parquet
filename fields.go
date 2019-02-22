package parquet

import (
	"bytes"

	"fmt"

	"encoding/binary"

	"io"

	"github.com/golang/snappy"
	sch "github.com/parsyl/parquet/generated"
)

// RequiredField writes the raw data for required columns
type IntStats struct {
	len int
}

func NewIntStats(len int) IntStats {
	return IntStats{len}
}

func (i IntStats) Statistics(min, max int64) *sch.Statistics {
	return &sch.Statistics{
		MinValue: i.minmax(min),
		MaxValue: i.minmax(min),
	}
}

func (i IntStats) minmax(val int64) []byte {
	buf := make([]byte, i.len)
	n := binary.PutVarint(buf, int64(val))
	return buf[:n]
}

type UintStats struct {
	len int
}

func NewUintStats(len int) UintStats {
	return UintStats{len}
}

func (i UintStats) Statistics(min, max uint64) *sch.Statistics {
	return &sch.Statistics{
		MinValue: i.minmax(min),
		MaxValue: i.minmax(min),
	}
}

func (i UintStats) minmax(val uint64) []byte {
	buf := make([]byte, i.len)
	n := binary.PutUvarint(buf, uint64(val))
	return buf[:n]
}

type RequiredField struct {
	col         string
	compression sch.CompressionCodec
}

// NewRequiredField creates a new required field.
func NewRequiredField(col string, opts ...func(*RequiredField)) RequiredField {
	r := RequiredField{col: col, compression: sch.CompressionCodec_SNAPPY}
	for _, opt := range opts {
		opt(&r)
	}
	return r
}

// RequiredFieldSnappy sets the compression for a column to snappy
// It is an optional arg to NewRequiredField
func RequiredFieldSnappy(r *RequiredField) {
	r.compression = sch.CompressionCodec_SNAPPY
}

// RequiredFieldUncompressed sets the compression to none
// It is an optional arg to NewRequiredField
func RequiredFieldUncompressed(r *RequiredField) {
	r.compression = sch.CompressionCodec_UNCOMPRESSED
}

// DoWrite writes the actual raw data.
func (f *RequiredField) DoWrite(w io.Writer, meta *Metadata, vals []byte, count int, stats *sch.Statistics) error {
	l, cl, vals := compress(f.compression, vals)
	if err := meta.WritePageHeader(w, f.col, l, cl, count, f.compression, stats); err != nil {
		return err
	}

	_, err := w.Write(vals)
	return err
}

func (f *RequiredField) DoRead(r io.ReadSeeker, meta *Metadata, pg Page) (io.Reader, []int, error) {
	var nRead int
	var out []byte
	var sizes []int
	for nRead < pg.N {
		ph, err := meta.PageHeader(r)
		if err != nil {
			return nil, nil, err
		}

		sizes = append(sizes, int(ph.DataPageHeader.NumValues))

		data, err := pageData(r, ph, pg)
		if err != nil {
			return nil, nil, err
		}

		out = append(out, data...)
		nRead += int(ph.DataPageHeader.NumValues)
	}

	return bytes.NewBuffer(out), sizes, nil
}

func (f *RequiredField) Name() string {
	return f.col
}

type OptionalField struct {
	Defs        []int64
	col         string
	compression sch.CompressionCodec
}

func NewOptionalField(col string, opts ...func(*OptionalField)) OptionalField {
	f := OptionalField{col: col, compression: sch.CompressionCodec_SNAPPY}
	for _, opt := range opts {
		opt(&f)
	}
	return f
}

// OptionalFieldSnappy sets the compression for a column to snappy
// It is an optional arg to NewOptionalField
func OptionalFieldSnappy(r *OptionalField) {
	r.compression = sch.CompressionCodec_SNAPPY
}

// OptionalFieldUncompressed sets the compression to none
// It is an optional arg to NewOptionalField
func OptionalFieldUncompressed(o *OptionalField) {
	o.compression = sch.CompressionCodec_UNCOMPRESSED
}

// Values reads the definition levels and uses them
// to return the values from the page data.
func (f *OptionalField) Values() int {
	return valsFromDefs(f.Defs)
}

func valsFromDefs(defs []int64) int {
	var out int
	for _, d := range defs {
		if d == 1 {
			out++
		}
	}
	return out
}

// DoWrite is called by all optional field types to write the definition levels
// and raw data to the io.Writer
func (f *OptionalField) DoWrite(w io.Writer, meta *Metadata, vals []byte, count int, stats *sch.Statistics) error {
	buf := bytes.Buffer{}
	wc := &writeCounter{w: &buf}
	err := writeLevels(wc, f.Defs)
	if err != nil {
		return err
	}

	wc.Write(vals)
	l, cl, vals := compress(f.compression, buf.Bytes())
	if err := meta.WritePageHeader(w, f.col, l, cl, len(f.Defs), f.compression, stats); err != nil {
		return err
	}
	_, err = w.Write(vals)
	return err
}

func (f *OptionalField) NilCount() *int64 {
	var out int64
	for _, v := range f.Defs {
		if v == 0 {
			out++
		}
	}
	return &out
}

// DoRead is called by all optional fields.  It reads the definition levels and uses
// them to interpret the raw data.
func (f *OptionalField) DoRead(r io.ReadSeeker, meta *Metadata, pg Page) (io.Reader, []int, error) {
	var nRead int
	var out []byte
	var sizes []int
	for nRead < pg.N {
		ph, err := meta.PageHeader(r)
		if err != nil {
			return nil, nil, err
		}

		data, err := pageData(r, ph, pg)
		if err != nil {
			return nil, nil, err
		}

		defs, l, err := readLevels(bytes.NewBuffer(data))
		if err != nil {
			return nil, nil, err
		}
		f.Defs = append(f.Defs, defs[:int(ph.DataPageHeader.NumValues)]...)
		sizes = append(sizes, valsFromDefs(defs))
		out = append(out, data[l:]...)
		nRead += int(ph.DataPageHeader.NumValues)
	}
	return bytes.NewBuffer(out), sizes, nil
}

// Name returns the column name of this field
func (f *OptionalField) Name() string {
	return f.col
}

// writeCounter keeps track of the number of bytes written
// it is used for calls to binary.Write, which does not
// return the number of bytes written.
type writeCounter struct {
	n int64
	w io.Writer
}

// Write makes writeCounter an io.Writer
func (w *writeCounter) Write(p []byte) (int, error) {
	n, err := w.w.Write(p)
	w.n += int64(n)
	return n, err
}

func pageData(r io.ReadSeeker, ph *sch.PageHeader, pg Page) ([]byte, error) {
	var data []byte
	switch pg.Codec {
	case sch.CompressionCodec_SNAPPY:
		compressed := make([]byte, ph.CompressedPageSize)
		if _, err := r.Read(compressed); err != nil {
			return nil, err
		}

		var err error
		data, err = snappy.Decode(nil, compressed)
		if err != nil {
			return nil, err
		}
	case sch.CompressionCodec_UNCOMPRESSED:
		data = make([]byte, ph.UncompressedPageSize)
		if _, err := r.Read(data); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported column chunk codec: %s", pg.Codec)
	}

	return data, nil
}

func compress(codec sch.CompressionCodec, vals []byte) (int, int, []byte) {
	var l, cl int
	switch codec {
	case sch.CompressionCodec_SNAPPY:
		l = len(vals)
		vals = snappy.Encode(nil, vals)
		cl = len(vals)
	case sch.CompressionCodec_UNCOMPRESSED:
		l = len(vals)
		cl = len(vals)
	}
	return l, cl, vals
}
