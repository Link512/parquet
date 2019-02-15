package parquet_test

// This code is generated by github.com/parsyl/parquet.

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"

	"github.com/parsyl/parquet"
)

// ParquetWriter reprents a row group
type ParquetWriter struct {
	fields []Field

	len int

	// child points to the next page
	child *ParquetWriter

	// max is the number of Record items that can get written before
	// a new set of column chunks is written
	max int

	meta *parquet.Metadata
	w    *parquet.WriteCounter
}

func Fields() []Field {
	return []Field{
		NewInt32Field(func(x Person) int32 { return x.ID }, func(x *Person, v int32) { x.ID = v }, "id"),
		NewInt32OptionalField(func(x Person) *int32 { return x.Age }, func(x *Person, v *int32) { x.Age = v }, "age"),
		NewInt64Field(func(x Person) int64 { return x.Happiness }, func(x *Person, v int64) { x.Happiness = v }, "happiness"),
		NewInt64OptionalField(func(x Person) *int64 { return x.Sadness }, func(x *Person, v *int64) { x.Sadness = v }, "sadness"),
		NewStringOptionalField(func(x Person) *string { return x.Code }, func(x *Person, v *string) { x.Code = v }, "code"),
		NewFloat32Field(func(x Person) float32 { return x.Funkiness }, func(x *Person, v float32) { x.Funkiness = v }, "funkiness"),
		NewFloat32OptionalField(func(x Person) *float32 { return x.Lameness }, func(x *Person, v *float32) { x.Lameness = v }, "lameness"),
		NewBoolOptionalField(func(x Person) *bool { return x.Keen }, func(x *Person, v *bool) { x.Keen = v }, "keen"),
		NewUint32Field(func(x Person) uint32 { return x.Birthday }, func(x *Person, v uint32) { x.Birthday = v }, "birthday"),
		NewUint64OptionalField(func(x Person) *uint64 { return x.Anniversary }, func(x *Person, v *uint64) { x.Anniversary = v }, "anniversary"),
		NewBoolField(func(x Person) bool { return x.Sleepy }, func(x *Person, v bool) { x.Sleepy = v }, "Sleepy"),
	}
}

func NewParquetWriter(w io.Writer, opts ...func(*ParquetWriter) error) (*ParquetWriter, error) {
	return newParquetWriter(w, append(opts, begin)...)
}

func newParquetWriter(w io.Writer, opts ...func(*ParquetWriter) error) (*ParquetWriter, error) {
	p := &ParquetWriter{
		max:    1000,
		w:      parquet.NewWriteCounter(w),
		fields: Fields(),
	}

	for _, opt := range opts {
		if err := opt(p); err != nil {
			return nil, err
		}
	}

	if p.meta == nil {
		ff := Fields()
		schema := make([]parquet.Field, len(ff))
		for i, f := range ff {
			schema[i] = f.Schema()
		}
		p.meta = parquet.New(schema...)
	}

	return p, nil
}

// MaxPageSize is the maximum number of rows in each row groups' page.
func MaxPageSize(m int) func(*ParquetWriter) error {
	return func(p *ParquetWriter) error {
		p.max = m
		return nil
	}
}

func begin(p *ParquetWriter) error {
	_, err := p.w.Write([]byte("PAR1"))
	return err
}

func withMeta(m *parquet.Metadata) func(*ParquetWriter) error {
	return func(p *ParquetWriter) error {
		p.meta = m
		return nil
	}
}

func (p *ParquetWriter) Write() error {
	for i, f := range p.fields {
		if err := f.Write(p.w, p.meta); err != nil {
			return err
		}

		for child := p.child; child != nil; child = child.child {
			if err := child.fields[i].Write(p.w, p.meta); err != nil {
				return err
			}
		}
	}

	p.fields = Fields()
	p.child = nil
	p.len = 0

	schema := make([]parquet.Field, len(p.fields))
	for i, f := range p.fields {
		schema[i] = f.Schema()
	}
	p.meta.StartRowGroup(schema...)
	return nil
}

func (p *ParquetWriter) Close() error {
	if err := p.meta.Footer(p.w); err != nil {
		return err
	}

	_, err := p.w.Write([]byte("PAR1"))
	return err
}

func (p *ParquetWriter) Add(rec Person) {
	if p.len == p.max {
		if p.child == nil {
			// an error can't happen here
			p.child, _ = newParquetWriter(p.w, MaxPageSize(p.max), withMeta(p.meta))
		}

		p.child.Add(rec)
		return
	}

	for _, f := range p.fields {
		f.Add(rec)
	}

	p.len++
}

type Field interface {
	Add(r Person)
	Write(w io.Writer, meta *parquet.Metadata) error
	Schema() parquet.Field
	Scan(r *Person)
	Read(r io.ReadSeeker, meta *parquet.Metadata, pos parquet.Position) error
	Name() string
}

func getFields(ff []Field) map[string]Field {
	m := make(map[string]Field, len(ff))
	for _, f := range ff {
		m[f.Name()] = f
	}
	return m
}

func NewParquetReader(r io.ReadSeeker, opts ...func(*ParquetReader)) (*ParquetReader, error) {
	ff := Fields()
	pr := &ParquetReader{
		r: r,
	}

	for _, opt := range opts {
		opt(pr)
	}

	schema := make([]parquet.Field, len(ff))
	for i, f := range ff {
		schema[i] = f.Schema()
	}

	meta := parquet.New(schema...)
	if err := meta.ReadFooter(r); err != nil {
		return nil, err
	}
	pr.rows = meta.Rows()
	var err error
	pr.offsets, err = meta.Offsets()
	if err != nil {
		return nil, err
	}

	pr.rowGroups = meta.RowGroups()
	_, err = r.Seek(4, io.SeekStart)
	if err != nil {
		return nil, err
	}
	pr.meta = meta

	return pr, pr.readRowGroup()
}

func readerIndex(i int) func(*ParquetReader) {
	return func(p *ParquetReader) {
		p.index = i
	}
}

// ParquetReader reads one page from a row group.
type ParquetReader struct {
	fields         map[string]Field
	index          int
	cursor         int64
	rows           int64
	rowGroupCursor int64
	rowGroupCount  int64
	offsets        map[string][]parquet.Position
	meta           *parquet.Metadata
	err            error

	r         io.ReadSeeker
	rowGroups []parquet.RowGroup
}

func (p *ParquetReader) Error() error {
	return p.err
}

func (p *ParquetReader) readRowGroup() error {
	p.rowGroupCursor = 0

	if len(p.rowGroups) == 0 {
		p.rowGroupCount = 0
		return nil
	}

	rg := p.rowGroups[0]
	p.fields = getFields(Fields())
	p.rowGroupCount = rg.Rows
	p.rowGroupCursor = 0
	for _, col := range rg.Columns() {
		name := col.MetaData.PathInSchema[len(col.MetaData.PathInSchema)-1]
		f, ok := p.fields[name]
		if !ok {
			return fmt.Errorf("unknown field: %s", name)
		}
		offsets := p.offsets[f.Name()]
		if len(offsets) <= p.index {
			break
		}

		o := offsets[0]
		if err := f.Read(p.r, p.meta, o); err != nil {
			return fmt.Errorf("unable to read field %s, err: %s", f.Name(), err)
		}
		p.offsets[f.Name()] = p.offsets[f.Name()][1:]
	}
	p.rowGroups = p.rowGroups[1:]
	return nil
}

func (p *ParquetReader) Rows() int64 {
	return p.rows
}

func (p *ParquetReader) Next() bool {
	if p.err == nil && p.cursor >= p.rows {
		return false
	}
	if p.rowGroupCursor >= p.rowGroupCount {
		p.err = p.readRowGroup()
		if p.err != nil {
			return false
		}
	}

	p.cursor++
	p.rowGroupCursor++
	return true
}

func (p *ParquetReader) Scan(x *Person) {
	if p.err != nil {
		return
	}

	for _, f := range p.fields {
		f.Scan(x)
	}
}

type Int32Field struct {
	vals []int32
	parquet.RequiredField
	val  func(r Person) int32
	read func(r *Person, v int32)
}

func NewInt32Field(val func(r Person) int32, read func(r *Person, v int32), col string) *Int32Field {
	return &Int32Field{
		val:           val,
		read:          read,
		RequiredField: parquet.NewRequiredField(col),
	}
}

func (f *Int32Field) Schema() parquet.Field {
	return parquet.Field{Name: f.Name(), Type: parquet.Int32Type, RepetitionType: parquet.RepetitionRequired}
}

func (f *Int32Field) Scan(r *Person) {
	if len(f.vals) == 0 {
		return
	}
	v := f.vals[0]
	f.vals = f.vals[1:]
	f.read(r, v)
}

func (f *Int32Field) Write(w io.Writer, meta *parquet.Metadata) error {
	var buf bytes.Buffer
	for _, v := range f.vals {
		if err := binary.Write(&buf, binary.LittleEndian, v); err != nil {
			return err
		}
	}
	return f.DoWrite(w, meta, buf.Bytes(), len(f.vals))
}

func (f *Int32Field) Read(r io.ReadSeeker, meta *parquet.Metadata, pos parquet.Position) error {
	rr, _, err := f.DoRead(r, meta, pos)
	if err != nil {
		return err
	}

	v := make([]int32, int(pos.N))
	err = binary.Read(rr, binary.LittleEndian, &v)
	f.vals = append(f.vals, v...)
	return err
}

func (f *Int32Field) Add(r Person) {
	f.vals = append(f.vals, f.val(r))
}

type Int32OptionalField struct {
	parquet.OptionalField
	vals []int32
	read func(r *Person, v *int32)
	val  func(r Person) *int32
}

func NewInt32OptionalField(val func(r Person) *int32, read func(r *Person, v *int32), col string) *Int32OptionalField {
	return &Int32OptionalField{
		val:           val,
		read:          read,
		OptionalField: parquet.NewOptionalField(col),
	}
}

func (f *Int32OptionalField) Schema() parquet.Field {
	return parquet.Field{Name: f.Name(), Type: parquet.Int32Type, RepetitionType: parquet.RepetitionOptional}
}

func (f *Int32OptionalField) Write(w io.Writer, meta *parquet.Metadata) error {
	var buf bytes.Buffer
	for _, v := range f.vals {
		if err := binary.Write(&buf, binary.LittleEndian, v); err != nil {
			return err
		}
	}
	return f.DoWrite(w, meta, buf.Bytes(), len(f.vals))
}

func (f *Int32OptionalField) Read(r io.ReadSeeker, meta *parquet.Metadata, pos parquet.Position) error {
	rr, _, err := f.DoRead(r, meta, pos)
	if err != nil {
		return err
	}

	v := make([]int32, f.Values()-len(f.vals))
	err = binary.Read(rr, binary.LittleEndian, &v)
	f.vals = append(f.vals, v...)
	return err
}

func (f *Int32OptionalField) Add(r Person) {
	v := f.val(r)
	if v != nil {
		f.vals = append(f.vals, *v)
		f.Defs = append(f.Defs, 1)
	} else {
		f.Defs = append(f.Defs, 0)
	}
}

func (f *Int32OptionalField) Scan(r *Person) {
	if len(f.Defs) == 0 {
		return
	}

	if f.Defs[0] == 1 {
		var val int32
		v := f.vals[0]
		f.vals = f.vals[1:]
		val = v
		f.read(r, &val)
	}
	f.Defs = f.Defs[1:]
}

type Int64Field struct {
	vals []int64
	parquet.RequiredField
	val  func(r Person) int64
	read func(r *Person, v int64)
}

func NewInt64Field(val func(r Person) int64, read func(r *Person, v int64), col string) *Int64Field {
	return &Int64Field{
		val:           val,
		read:          read,
		RequiredField: parquet.NewRequiredField(col),
	}
}

func (f *Int64Field) Schema() parquet.Field {
	return parquet.Field{Name: f.Name(), Type: parquet.Int64Type, RepetitionType: parquet.RepetitionRequired}
}

func (f *Int64Field) Scan(r *Person) {
	if len(f.vals) == 0 {
		return
	}
	v := f.vals[0]
	f.vals = f.vals[1:]
	f.read(r, v)
}

func (f *Int64Field) Write(w io.Writer, meta *parquet.Metadata) error {
	var buf bytes.Buffer
	for _, v := range f.vals {
		if err := binary.Write(&buf, binary.LittleEndian, v); err != nil {
			return err
		}
	}
	return f.DoWrite(w, meta, buf.Bytes(), len(f.vals))
}

func (f *Int64Field) Read(r io.ReadSeeker, meta *parquet.Metadata, pos parquet.Position) error {
	rr, _, err := f.DoRead(r, meta, pos)
	if err != nil {
		return err
	}

	v := make([]int64, int(pos.N))
	err = binary.Read(rr, binary.LittleEndian, &v)
	f.vals = append(f.vals, v...)
	return err
}

func (f *Int64Field) Add(r Person) {
	f.vals = append(f.vals, f.val(r))
}

type Int64OptionalField struct {
	parquet.OptionalField
	vals []int64
	read func(r *Person, v *int64)
	val  func(r Person) *int64
}

func NewInt64OptionalField(val func(r Person) *int64, read func(r *Person, v *int64), col string) *Int64OptionalField {
	return &Int64OptionalField{
		val:           val,
		read:          read,
		OptionalField: parquet.NewOptionalField(col),
	}
}

func (f *Int64OptionalField) Schema() parquet.Field {
	return parquet.Field{Name: f.Name(), Type: parquet.Int64Type, RepetitionType: parquet.RepetitionOptional}
}

func (f *Int64OptionalField) Write(w io.Writer, meta *parquet.Metadata) error {
	var buf bytes.Buffer
	for _, v := range f.vals {
		if err := binary.Write(&buf, binary.LittleEndian, v); err != nil {
			return err
		}
	}
	return f.DoWrite(w, meta, buf.Bytes(), len(f.vals))
}

func (f *Int64OptionalField) Read(r io.ReadSeeker, meta *parquet.Metadata, pos parquet.Position) error {
	rr, _, err := f.DoRead(r, meta, pos)
	if err != nil {
		return err
	}

	v := make([]int64, f.Values()-len(f.vals))
	err = binary.Read(rr, binary.LittleEndian, &v)
	f.vals = append(f.vals, v...)
	return err
}

func (f *Int64OptionalField) Add(r Person) {
	v := f.val(r)
	if v != nil {
		f.vals = append(f.vals, *v)
		f.Defs = append(f.Defs, 1)
	} else {
		f.Defs = append(f.Defs, 0)
	}
}

func (f *Int64OptionalField) Scan(r *Person) {
	if len(f.Defs) == 0 {
		return
	}

	if f.Defs[0] == 1 {
		var val int64
		v := f.vals[0]
		f.vals = f.vals[1:]
		val = v
		f.read(r, &val)
	}
	f.Defs = f.Defs[1:]
}

type StringOptionalField struct {
	parquet.OptionalField
	vals []string
	val  func(r Person) *string
	read func(r *Person, v *string)
}

func NewStringOptionalField(val func(r Person) *string, read func(r *Person, v *string), col string) *StringOptionalField {
	return &StringOptionalField{
		val:           val,
		read:          read,
		OptionalField: parquet.NewOptionalField(col),
	}
}

func (f *StringOptionalField) Schema() parquet.Field {
	return parquet.Field{Name: f.Name(), Type: parquet.StringType, RepetitionType: parquet.RepetitionOptional}
}

func (f *StringOptionalField) Scan(r *Person) {
	if len(f.Defs) == 0 {
		return
	}

	if f.Defs[0] == 1 {
		var val *string
		v := f.vals[0]
		f.vals = f.vals[1:]
		val = &v
		f.read(r, val)
	}
	f.Defs = f.Defs[1:]
}

func (f *StringOptionalField) Add(r Person) {
	v := f.val(r)
	if v != nil {
		f.vals = append(f.vals, *v)
		f.Defs = append(f.Defs, 1)
	} else {
		f.Defs = append(f.Defs, 0)
	}
}

func (f *StringOptionalField) Write(w io.Writer, meta *parquet.Metadata) error {
	buf := bytes.Buffer{}

	for _, s := range f.vals {
		if err := binary.Write(&buf, binary.LittleEndian, int32(len(s))); err != nil {
			return err
		}
		buf.Write([]byte(s))
	}

	return f.DoWrite(w, meta, buf.Bytes(), len(f.vals))
}

func (f *StringOptionalField) Read(r io.ReadSeeker, meta *parquet.Metadata, pos parquet.Position) error {
	start := len(f.Defs)
	rr, _, err := f.DoRead(r, meta, pos)
	if err != nil {
		return err
	}

	for j := 0; j < pos.N; j++ {
		if f.Defs[start+j] == 0 {
			continue
		}

		var x int32
		if err := binary.Read(rr, binary.LittleEndian, &x); err != nil {
			return err
		}
		s := make([]byte, x)
		if _, err := rr.Read(s); err != nil {
			return err
		}

		f.vals = append(f.vals, string(s))
	}
	return nil
}

type Float32Field struct {
	vals []float32
	parquet.RequiredField
	val  func(r Person) float32
	read func(r *Person, v float32)
}

func NewFloat32Field(val func(r Person) float32, read func(r *Person, v float32), col string) *Float32Field {
	return &Float32Field{
		val:           val,
		read:          read,
		RequiredField: parquet.NewRequiredField(col),
	}
}

func (f *Float32Field) Schema() parquet.Field {
	return parquet.Field{Name: f.Name(), Type: parquet.Float32Type, RepetitionType: parquet.RepetitionRequired}
}

func (f *Float32Field) Scan(r *Person) {
	if len(f.vals) == 0 {
		return
	}
	v := f.vals[0]
	f.vals = f.vals[1:]
	f.read(r, v)
}

func (f *Float32Field) Write(w io.Writer, meta *parquet.Metadata) error {
	var buf bytes.Buffer
	for _, v := range f.vals {
		if err := binary.Write(&buf, binary.LittleEndian, v); err != nil {
			return err
		}
	}
	return f.DoWrite(w, meta, buf.Bytes(), len(f.vals))
}

func (f *Float32Field) Read(r io.ReadSeeker, meta *parquet.Metadata, pos parquet.Position) error {
	rr, _, err := f.DoRead(r, meta, pos)
	if err != nil {
		return err
	}

	v := make([]float32, int(pos.N))
	err = binary.Read(rr, binary.LittleEndian, &v)
	f.vals = append(f.vals, v...)
	return err
}

func (f *Float32Field) Add(r Person) {
	f.vals = append(f.vals, f.val(r))
}

type Float32OptionalField struct {
	parquet.OptionalField
	vals []float32
	read func(r *Person, v *float32)
	val  func(r Person) *float32
}

func NewFloat32OptionalField(val func(r Person) *float32, read func(r *Person, v *float32), col string) *Float32OptionalField {
	return &Float32OptionalField{
		val:           val,
		read:          read,
		OptionalField: parquet.NewOptionalField(col),
	}
}

func (f *Float32OptionalField) Schema() parquet.Field {
	return parquet.Field{Name: f.Name(), Type: parquet.Float32Type, RepetitionType: parquet.RepetitionOptional}
}

func (f *Float32OptionalField) Write(w io.Writer, meta *parquet.Metadata) error {
	var buf bytes.Buffer
	for _, v := range f.vals {
		if err := binary.Write(&buf, binary.LittleEndian, v); err != nil {
			return err
		}
	}
	return f.DoWrite(w, meta, buf.Bytes(), len(f.vals))
}

func (f *Float32OptionalField) Read(r io.ReadSeeker, meta *parquet.Metadata, pos parquet.Position) error {
	rr, _, err := f.DoRead(r, meta, pos)
	if err != nil {
		return err
	}

	v := make([]float32, f.Values()-len(f.vals))
	err = binary.Read(rr, binary.LittleEndian, &v)
	f.vals = append(f.vals, v...)
	return err
}

func (f *Float32OptionalField) Add(r Person) {
	v := f.val(r)
	if v != nil {
		f.vals = append(f.vals, *v)
		f.Defs = append(f.Defs, 1)
	} else {
		f.Defs = append(f.Defs, 0)
	}
}

func (f *Float32OptionalField) Scan(r *Person) {
	if len(f.Defs) == 0 {
		return
	}

	if f.Defs[0] == 1 {
		var val float32
		v := f.vals[0]
		f.vals = f.vals[1:]
		val = v
		f.read(r, &val)
	}
	f.Defs = f.Defs[1:]
}

type BoolOptionalField struct {
	parquet.OptionalField
	vals []bool
	val  func(r Person) *bool
	read func(r *Person, v *bool)
}

func NewBoolOptionalField(val func(r Person) *bool, read func(r *Person, v *bool), col string) *BoolOptionalField {
	return &BoolOptionalField{
		val:           val,
		read:          read,
		OptionalField: parquet.NewOptionalField(col),
	}
}

func (f *BoolOptionalField) Schema() parquet.Field {
	return parquet.Field{Name: f.Name(), Type: parquet.BoolType, RepetitionType: parquet.RepetitionOptional}
}

func (f *BoolOptionalField) Read(r io.ReadSeeker, meta *parquet.Metadata, pos parquet.Position) error {
	rr, sizes, err := f.DoRead(r, meta, pos)
	if err != nil {
		return err
	}

	v, err := parquet.GetBools(rr, f.Values()-len(f.vals), sizes)
	f.vals = append(f.vals, v...)
	return err
}

func (f *BoolOptionalField) Scan(r *Person) {
	if len(f.Defs) == 0 {
		return
	}

	var val *bool
	if f.Defs[0] == 1 {
		v := f.vals[0]
		f.vals = f.vals[1:]
		val = &v
		f.read(r, val)
	}
	f.Defs = f.Defs[1:]
}

func (f *BoolOptionalField) Add(r Person) {
	v := f.val(r)
	if v != nil {
		f.vals = append(f.vals, *v)
		f.Defs = append(f.Defs, 1)
	} else {
		f.Defs = append(f.Defs, 0)
	}
}

func (f *BoolOptionalField) Write(w io.Writer, meta *parquet.Metadata) error {
	ln := len(f.vals)
	byteNum := (ln + 7) / 8
	rawBuf := make([]byte, byteNum)

	for i := 0; i < ln; i++ {
		if f.vals[i] {
			rawBuf[i/8] = rawBuf[i/8] | (1 << uint32(i%8))
		}
	}

	return f.DoWrite(w, meta, rawBuf, len(f.vals))
}

type Uint32Field struct {
	vals []uint32
	parquet.RequiredField
	val  func(r Person) uint32
	read func(r *Person, v uint32)
}

func NewUint32Field(val func(r Person) uint32, read func(r *Person, v uint32), col string) *Uint32Field {
	return &Uint32Field{
		val:           val,
		read:          read,
		RequiredField: parquet.NewRequiredField(col),
	}
}

func (f *Uint32Field) Schema() parquet.Field {
	return parquet.Field{Name: f.Name(), Type: parquet.Uint32Type, RepetitionType: parquet.RepetitionRequired}
}

func (f *Uint32Field) Scan(r *Person) {
	if len(f.vals) == 0 {
		return
	}
	v := f.vals[0]
	f.vals = f.vals[1:]
	f.read(r, v)
}

func (f *Uint32Field) Write(w io.Writer, meta *parquet.Metadata) error {
	var buf bytes.Buffer
	for _, v := range f.vals {
		if err := binary.Write(&buf, binary.LittleEndian, v); err != nil {
			return err
		}
	}
	return f.DoWrite(w, meta, buf.Bytes(), len(f.vals))
}

func (f *Uint32Field) Read(r io.ReadSeeker, meta *parquet.Metadata, pos parquet.Position) error {
	rr, _, err := f.DoRead(r, meta, pos)
	if err != nil {
		return err
	}

	v := make([]uint32, int(pos.N))
	err = binary.Read(rr, binary.LittleEndian, &v)
	f.vals = append(f.vals, v...)
	return err
}

func (f *Uint32Field) Add(r Person) {
	f.vals = append(f.vals, f.val(r))
}

type Uint64OptionalField struct {
	parquet.OptionalField
	vals []uint64
	read func(r *Person, v *uint64)
	val  func(r Person) *uint64
}

func NewUint64OptionalField(val func(r Person) *uint64, read func(r *Person, v *uint64), col string) *Uint64OptionalField {
	return &Uint64OptionalField{
		val:           val,
		read:          read,
		OptionalField: parquet.NewOptionalField(col),
	}
}

func (f *Uint64OptionalField) Schema() parquet.Field {
	return parquet.Field{Name: f.Name(), Type: parquet.Uint64Type, RepetitionType: parquet.RepetitionOptional}
}

func (f *Uint64OptionalField) Write(w io.Writer, meta *parquet.Metadata) error {
	var buf bytes.Buffer
	for _, v := range f.vals {
		if err := binary.Write(&buf, binary.LittleEndian, v); err != nil {
			return err
		}
	}
	return f.DoWrite(w, meta, buf.Bytes(), len(f.vals))
}

func (f *Uint64OptionalField) Read(r io.ReadSeeker, meta *parquet.Metadata, pos parquet.Position) error {
	rr, _, err := f.DoRead(r, meta, pos)
	if err != nil {
		return err
	}

	v := make([]uint64, f.Values()-len(f.vals))
	err = binary.Read(rr, binary.LittleEndian, &v)
	f.vals = append(f.vals, v...)
	return err
}

func (f *Uint64OptionalField) Add(r Person) {
	v := f.val(r)
	if v != nil {
		f.vals = append(f.vals, *v)
		f.Defs = append(f.Defs, 1)
	} else {
		f.Defs = append(f.Defs, 0)
	}
}

func (f *Uint64OptionalField) Scan(r *Person) {
	if len(f.Defs) == 0 {
		return
	}

	if f.Defs[0] == 1 {
		var val uint64
		v := f.vals[0]
		f.vals = f.vals[1:]
		val = v
		f.read(r, &val)
	}
	f.Defs = f.Defs[1:]
}

type BoolField struct {
	parquet.RequiredField
	vals []bool
	val  func(r Person) bool
	read func(r *Person, v bool)
}

func NewBoolField(val func(r Person) bool, read func(r *Person, v bool), col string) *BoolField {
	return &BoolField{
		val:           val,
		read:          read,
		RequiredField: parquet.NewRequiredField(col),
	}
}

func (f *BoolField) Schema() parquet.Field {
	return parquet.Field{Name: f.Name(), Type: parquet.BoolType, RepetitionType: parquet.RepetitionRequired}
}

func (f *BoolField) Scan(r *Person) {
	if len(f.vals) == 0 {
		return
	}

	v := f.vals[0]
	f.vals = f.vals[1:]
	f.read(r, v)
}

func (f *BoolField) Add(r Person) {
	f.vals = append(f.vals, f.val(r))
}

func (f *BoolField) Write(w io.Writer, meta *parquet.Metadata) error {
	ln := len(f.vals)
	byteNum := (ln + 7) / 8
	rawBuf := make([]byte, byteNum)

	for i := 0; i < ln; i++ {
		if f.vals[i] {
			rawBuf[i/8] = rawBuf[i/8] | (1 << uint32(i%8))
		}
	}

	return f.DoWrite(w, meta, rawBuf, len(f.vals))
}

func (f *BoolField) Read(r io.ReadSeeker, meta *parquet.Metadata, pos parquet.Position) error {
	rr, sizes, err := f.DoRead(r, meta, pos)
	if err != nil {
		return err
	}

	f.vals, err = parquet.GetBools(rr, int(pos.N), sizes)
	return err
}
