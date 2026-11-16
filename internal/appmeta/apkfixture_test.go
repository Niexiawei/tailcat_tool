package appmeta

// A minimal APK built at test time, so the store tests parse a real binary
// AndroidManifest.xml without shipping an APK in the repository.

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"testing"
	"unicode/utf16"
)

const (
	chunkStringPool = 0x0001
	chunkXMLFile    = 0x0003
	chunkXMLNsStart = 0x0100
	chunkXMLNsEnd   = 0x0101
	chunkXMLStart   = 0x0102
	chunkXMLEnd     = 0x0103

	// attrTypeString is Res_value's TYPE_STRING.
	attrTypeString = 0x03
)

// testPNG is a 67-byte 1×1 PNG: enough for the store to read an icon.
var testPNG = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d,
	0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4, 0x89, 0x00, 0x00, 0x00,
	0x0a, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00,
	0x05, 0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00, 0x00, 0x00, 0x00, 0x49,
	0x45, 0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
}

// buildTestAPK writes an APK whose manifest declares one <application> with a
// literal label and icon, plus the icon file itself.
func buildTestAPK(t *testing.T, label, iconPath string, icon []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	if err := writeZipFile(zw, "AndroidManifest.xml", buildTestManifest(label, iconPath)); err != nil {
		t.Fatalf("writing manifest: %v", err)
	}
	if iconPath != "" && icon != nil {
		if err := writeZipFile(zw, iconPath, icon); err != nil {
			t.Fatalf("writing icon: %v", err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("closing the test apk: %v", err)
	}
	return buf.Bytes()
}

func writeZipFile(zw *zip.Writer, name string, data []byte) error {
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

// axml is the small subset of Android's binary XML the manifest needs: a
// string pool, one namespace pair, and elements with string attributes.
type axml struct {
	pool    []string
	indices map[string]uint32
	body    []byte
}

func (a *axml) str(s string) uint32 {
	if a.indices == nil {
		a.indices = map[string]uint32{}
	}
	if idx, ok := a.indices[s]; ok {
		return idx
	}
	idx := uint32(len(a.pool))
	a.pool = append(a.pool, s)
	a.indices[s] = idx
	return idx
}

func (a *axml) raw(b []byte) { a.body = append(a.body, b...) }

func (a *axml) nsChunk(kind uint16, prefix, uri string) {
	var b bytes.Buffer
	chunkHeader(&b, kind, 16, 24)
	binary.Write(&b, binary.LittleEndian, []uint32{1, 0xFFFFFFFF, a.str(prefix), a.str(uri)})
	a.raw(b.Bytes())
}

// chunkHeader writes the ResChunk_header every AXML chunk starts with: a type,
// the header's own size, and the chunk's total size.
func chunkHeader(b *bytes.Buffer, kind, headerSize uint16, size uint32) {
	binary.Write(b, binary.LittleEndian, kind)
	binary.Write(b, binary.LittleEndian, headerSize)
	binary.Write(b, binary.LittleEndian, size)
}

type axmlAttr struct {
	ns, name, value uint32
}

func (a *axml) start(name string, attrs []axmlAttr) {
	size := 16 + 20 + 20*len(attrs)
	var b bytes.Buffer
	chunkHeader(&b, chunkXMLStart, 16, uint32(size))
	// line number, comment, namespace, name, then the attribute extent header.
	binary.Write(&b, binary.LittleEndian, []uint32{1, 0xFFFFFFFF, 0xFFFFFFFF, a.str(name)})
	binary.Write(&b, binary.LittleEndian, []uint16{20, 20, uint16(len(attrs)), 0, 0, 0})
	for _, attr := range attrs {
		binary.Write(&b, binary.LittleEndian, []uint32{attr.ns, attr.name, attr.value})
		// Res_value: size, res0, dataType, then the data word.
		binary.Write(&b, binary.LittleEndian, uint16(8))
		b.WriteByte(0)
		b.WriteByte(attrTypeString)
		binary.Write(&b, binary.LittleEndian, attr.value)
	}
	a.raw(b.Bytes())
}

func (a *axml) end(name string) {
	var b bytes.Buffer
	chunkHeader(&b, chunkXMLEnd, 16, 24)
	binary.Write(&b, binary.LittleEndian, []uint32{1, 0xFFFFFFFF, 0xFFFFFFFF, a.str(name)})
	a.raw(b.Bytes())
}

func (a *axml) bytes() []byte {
	pool := buildStringPool(a.pool)
	total := 8 + len(pool) + len(a.body)
	var b bytes.Buffer
	chunkHeader(&b, chunkXMLFile, 8, uint32(total))
	b.Write(pool)
	b.Write(a.body)
	return b.Bytes()
}

// buildStringPool encodes UTF-16 strings, the simple format with no style data.
func buildStringPool(strs []string) []byte {
	var data bytes.Buffer
	offsets := make([]uint32, len(strs))
	for i, s := range strs {
		offsets[i] = uint32(data.Len())
		chars := utf16.Encode([]rune(s))
		binary.Write(&data, binary.LittleEndian, uint16(len(chars)))
		for _, c := range chars {
			binary.Write(&data, binary.LittleEndian, c)
		}
		binary.Write(&data, binary.LittleEndian, uint16(0))
	}
	for data.Len()%4 != 0 {
		data.WriteByte(0)
	}
	size := 28 + 4*len(strs) + data.Len()
	var b bytes.Buffer
	chunkHeader(&b, chunkStringPool, 28, uint32(size))
	binary.Write(&b, binary.LittleEndian, []uint32{uint32(len(strs)), 0, 0, uint32(28 + 4*len(strs)), 0})
	for _, off := range offsets {
		binary.Write(&b, binary.LittleEndian, off)
	}
	b.Write(data.Bytes())
	return b.Bytes()
}

func buildTestManifest(label, iconPath string) []byte {
	const ns = "http://schemas.android.com/apk/res/android"
	a := &axml{}
	// The URI index has to exist before the namespace chunk writes it.
	uri := a.str(ns)
	a.nsChunk(chunkXMLNsStart, "android", ns)
	a.start("manifest", []axmlAttr{{ns: 0xFFFFFFFF, name: a.str("package"), value: a.str("com.example.test")}})
	attrs := []axmlAttr{{ns: uri, name: a.str("label"), value: a.str(label)}}
	if iconPath != "" {
		attrs = append(attrs, axmlAttr{ns: uri, name: a.str("icon"), value: a.str(iconPath)})
	}
	a.start("application", attrs)
	a.end("application")
	a.end("manifest")
	a.nsChunk(chunkXMLNsEnd, "android", ns)
	return a.bytes()
}
