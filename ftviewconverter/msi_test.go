package ftviewconverter

import (
	"encoding/base64"
	"encoding/binary"
	"testing"
	"unicode/utf16"
)

// buildEntry builds one MSI entry: 4-byte little-endian entry_len
// (inclusive of itself), followed by name (UTF-16LE, ASCII-only), then
// payload bytes.
func buildEntry(name string, payload []byte) []byte {
	units := utf16.Encode([]rune(name))
	nameBytes := make([]byte, len(units)*2)
	for i, u := range units {
		binary.LittleEndian.PutUint16(nameBytes[i*2:], u)
	}
	body := append(nameBytes, payload...)
	entryLen := 4 + len(body)
	buf := make([]byte, 4)
	binary.LittleEndian.PutUint32(buf, uint32(entryLen))
	return append(buf, body...)
}

func TestMsiRoundTripDecode(t *testing.T) {
	// header: 16 bytes, content doesn't matter for the parser.
	blob := make([]byte, 16)

	// border = 1 (line)
	borderPayload := make([]byte, 4)
	binary.LittleEndian.PutUint32(borderPayload, 1)
	blob = append(blob, buildEntry("border", borderPayload)...)

	// currentstate = 0
	curPayload := make([]byte, 2)
	binary.LittleEndian.PutUint16(curPayload, 0)
	blob = append(blob, buildEntry("currentstate", curPayload)...)

	// state "0" text = "ON" -> charCount(4 bytes) + UTF-16LE chars
	text := "ON"
	textUnits := utf16.Encode([]rune(text))
	textPayload := make([]byte, 4+len(textUnits)*2)
	binary.LittleEndian.PutUint32(textPayload[0:4], uint32(len(textUnits)))
	for i, u := range textUnits {
		binary.LittleEndian.PutUint16(textPayload[4+i*2:], u)
	}
	blob = append(blob, buildEntry("0", textPayload)...)

	// statealignment0 (2-byte, since not "-1") = 4 (middleCenter)
	alignPayload := make([]byte, 2)
	binary.LittleEndian.PutUint16(alignPayload, 4)
	blob = append(blob, buildEntry("statealignment0", alignPayload)...)

	// statebackcolor0: non-system RGB (0,255,0) green
	backPayload := make([]byte, 4)
	binary.LittleEndian.PutUint32(backPayload, 0x0000FF00) // B=0x00,G=0xFF,R=0x00 (little-endian layout R,G,B,flags)
	blob = append(blob, buildEntry("statebackcolor0", backPayload)...)

	// statefont0: GUID + styleFlags(4 bytes, use byte[3]) + weight(2) + size(4) + nameLen(1) + name
	fontPayload := make([]byte, 0)
	fontPayload = append(fontPayload, stdFontGUID...)
	styleFlags := make([]byte, 4)
	styleFlags[3] = 0x02 // italic
	fontPayload = append(fontPayload, styleFlags...)
	weight := make([]byte, 2)
	binary.LittleEndian.PutUint16(weight, 700) // bold
	fontPayload = append(fontPayload, weight...)
	size := make([]byte, 4)
	binary.LittleEndian.PutUint32(size, 120000) // 12pt * 10000
	fontPayload = append(fontPayload, size...)
	fontName := "Tahoma"
	fontPayload = append(fontPayload, byte(len(fontName)))
	fontPayload = append(fontPayload, []byte(fontName)...)
	// last entry: truncated (no trailer), matches real samples
	blob = append(blob, buildEntry("statefont0", fontPayload)...)

	entries, ok := parseMsiEntries(blob)
	if !ok {
		t.Fatalf("parseMsiEntries failed")
	}
	if _, ok := entries["border"]; !ok {
		t.Errorf("missing border entry, got keys: %v", keysOf(entries))
	}

	data, ok := interpretMsiEntries(entries)
	if !ok {
		t.Fatalf("interpretMsiEntries failed")
	}
	if data.Border != 1 {
		t.Errorf("Border = %d, want 1", data.Border)
	}
	s, ok := data.States["0"]
	if !ok {
		t.Fatalf("state 0 missing")
	}
	if s.Text != "ON" {
		t.Errorf("Text = %q, want ON", s.Text)
	}
	if s.Alignment != "middleCenter" {
		t.Errorf("Alignment = %q, want middleCenter", s.Alignment)
	}
	if s.BackColor == nil || s.BackColor.G != 0xFF {
		t.Errorf("BackColor not decoded correctly: %+v", s.BackColor)
	}
	if s.Font == nil {
		t.Fatalf("Font not decoded")
	}
	if s.Font.FontName != "Tahoma" || !s.Font.Bold || !s.Font.Italic {
		t.Errorf("Font decoded incorrectly: %+v", s.Font)
	}
	if s.Font.SizePt != 12.0 {
		t.Errorf("SizePt = %v, want 12.0", s.Font.SizePt)
	}

	// Now exercise the full activeX -> multistateIndicator path.
	b64 := base64.StdEncoding.EncodeToString(blob)
	activeXNode := newXNode("activeX")
	activeXNode.SetAttr("name", "MSI1")
	activeXNode.SetAttr("classId", multistateIndicatorClassID)
	activeXNode.SetAttr("height", "50")
	activeXNode.SetAttr("width", "80")
	activeXNode.SetAttr("left", "10")
	activeXNode.SetAttr("top", "10")
	dataNode := newXNode("data")
	dataNode.SetAttr("data", b64)
	activeXNode.Children = append(activeXNode.Children, dataNode)

	connectionsNode := newXNode("connections")
	connNode := newXNode("connection")
	connNode.SetAttr("name", "IndicatorTag")
	connNode.SetAttr("expression", "MyTag.Value")
	connectionsNode.Children = append(connectionsNode.Children, connNode)
	activeXNode.Children = append(activeXNode.Children, connectionsNode)

	c := New()
	ok2 := c.tryConvertMultistateIndicatorSeToMe(activeXNode)
	if !ok2 {
		t.Fatalf("tryConvertMultistateIndicatorSeToMe failed")
	}
	if activeXNode.Tag != "multistateIndicator" {
		t.Errorf("Tag = %q, want multistateIndicator", activeXNode.Tag)
	}
	out := serialize(activeXNode)
	t.Logf("decoded multistateIndicator:\n%s", out)
}

func keysOf(m map[string][]byte) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}
