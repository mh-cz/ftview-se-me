package ftviewconverter

import (
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"unicode/utf16"
)

// ---------------------------------------------------------------------
// MultiState Indicator binary property-bag decoder (SE -> ME only).
//
// Format summary (see the reverse-engineering guide for full detail):
//   - Outer header: 16 bytes (4-byte unexplained marker, 4-byte total
//     blob length, 8-byte leader) before the first entry.
//   - Flat sequence of name/value entries, each starting with a 4-byte
//     little-endian entry_len INCLUSIVE of itself, followed by the
//     property's name in UTF-16LE with no explicit length prefix.
//   - Every real sample seen has its LAST entry (always the highest
//     numbered state's "statefontN") missing its trailing 8-byte
//     tag1/tag2/checksum trailer - the blob just ends after the font
//     name. This is not sample-specific corruption: it was confirmed
//     across every sample in the available corpus, so the parser below
//     treats a too-short/negative entry_len at the tail of the blob as
//     "last entry, read to end" rather than a fatal error.
// ---------------------------------------------------------------------

var stdFontGUID = []byte{
	0x03, 0x52, 0xe3, 0x0b, 0x91, 0x8f, 0xce, 0x11,
	0x9d, 0xe3, 0x00, 0xaa, 0x00, 0x4b, 0xb8, 0x51,
}

var alignmentNames = []string{
	"topLeft", "topCenter", "topRight",
	"middleLeft", "middleCenter", "middleRight",
	"bottomLeft", "bottomCenter", "bottomRight",
}

type msiFont struct {
	FontName      string
	SizePt        float64
	Bold          bool
	Italic        bool
	Underline     bool
	Strikethrough bool
}

func defaultMsiFont() msiFont {
	return msiFont{FontName: "Arial", SizePt: 8.25}
}

type msiColor struct {
	IsSystemColor    bool
	SystemColorIndex int
	R, G, B          byte
}

type msiState struct {
	Text      string
	Alignment string
	FontColor *msiColor
	BackColor *msiColor
	Font      *msiFont
}

func newMsiState() msiState {
	return msiState{Alignment: "middleCenter"}
}

type msiData struct {
	Border       int // Line is the confirmed default, omitted when selected
	CurrentState int16
	States       map[string]msiState
}

// tryConvertMultistateIndicatorSeToMe attempts to decode the <activeX>'s
// base64 blob and, on success, rewrites `node` in place into a
// <multistateIndicator> element. On any parsing failure this leaves
// `node` completely untouched and returns false so the caller can fall
// back to opaque passthrough.
func (c *Converter) tryConvertMultistateIndicatorSeToMe(node *XNode) bool {
	dataNode := node.findChild("data")
	b64 := ""
	if dataNode != nil {
		b64 = dataNode.GetAttr("data", "")
	}
	if dataNode == nil || len(b64) == 0 {
		return false
	}

	blob, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return false
	}

	entries, ok := parseMsiEntries(blob)
	if !ok {
		return false
	}

	data, ok := interpretMsiEntries(entries)
	if !ok {
		return false
	}

	name := node.GetAttr("name", "")
	height := node.GetAttr("height", "")
	width := node.GetAttr("width", "")
	left := node.GetAttr("left", "")
	top := node.GetAttr("top", "")
	visible := node.GetAttr("visible", "true")

	// The <connections> element is a sibling of <data>, not a source of
	// the binary blob at all - per the guide's architectural finding, tag
	// bindings never touch the OCX property bag, so there's nothing to
	// decode here. It just needs to be captured BEFORE node.Children is
	// cleared below, or it's lost entirely.
	connectionsNode := node.findChild("connections")
	indicatorTagExpr := ""
	if connectionsNode != nil {
		for _, ch := range connectionsNode.Children {
			if ch.Tag == "connection" && ch.GetAttr("name", "") == "IndicatorTag" {
				indicatorTagExpr = ch.GetAttr("expression", "")
				break
			}
		}
	}

	// <animations>/<animateVisibility> is likewise a sibling of <data>,
	// not part of the binary blob - and, unlike SE's other per-object
	// animation types (animateHeight/animateWidth/etc, which really do
	// have no ME equivalent - see seNoDirectMeEquivalentTags),
	// animateVisibility is natively supported by ME with the exact same
	// shape: <animations><animateVisibility expression="..."
	// expressionTrueState="visible|invisible"/></animations> (confirmed
	// byte-for-byte identical between an SE sample and a real ME
	// multistateIndicator export). It just needs to be captured BEFORE
	// node.ClearChildren() below (same reasoning as connectionsNode) so
	// it can be re-attached to the rebuilt node rather than lost along
	// with the rest of the wiped-out SE structure.
	animationsNode := node.findChild("animations")

	node.Tag = "multistateIndicator"
	node.ClearAttrs()
	node.ClearChildren()

	node.SetAttr("name", name)
	node.SetAttr("height", height)
	node.SetAttr("width", width)
	node.SetAttr("left", left)
	node.SetAttr("top", top)
	node.SetAttr("visible", visible)
	node.SetAttr("wallpaper", "false")
	node.SetAttr("isReferenceObject", "false")
	node.SetAttr("backStyle", "solid")
	// border: 0=No Border, 1=Line (default, omitted on the wire), 2=Inset.
	// ME's own "Border style" dropdown has direct, exact-name matches for
	// all three SE options (None/Line/Inset), alongside two extra members
	// with no SE equivalent (Raised/RaisedInset) - confirmed from the ME
	// property editor's own dropdown, not just inferred from XML samples.
	// No guessing/fallback needed for any SE value.
	var borderStyle string
	switch data.Border {
	case 0:
		borderStyle = "none"
	case 1:
		borderStyle = "line"
	case 2:
		borderStyle = "inset"
	default:
		borderStyle = "none"
	}
	node.SetAttr("borderStyle", borderStyle)
	node.SetAttr("borderUsesBackColor", "true")
	node.SetAttr("borderWidth", "8")
	node.SetAttr("description", "")
	node.SetAttr("shape", "rectangle")
	node.SetAttr("triggerType", "value")
	node.SetAttr("currentStateId", strconv.Itoa(int(data.CurrentState)))
	node.SetAttr("captionOnBorder", "false")

	statesNode := newXNode("states")
	node.Children = append(node.Children, statesNode)

	// Emit in Error-then-ascending order, covering however many states
	// this particular control actually has (not just 0-3 - see
	// discoverStateIDs).
	for _, stateID := range discoverStateIDs(entries) {
		s, ok := data.States[stateID]
		if !ok {
			continue
		}

		stateNode := newXNode("state")
		if stateID == "-1" {
			stateNode.SetAttr("stateId", "Error")
		} else {
			stateNode.SetAttr("stateId", stateID)
			stateNode.SetAttr("value", stateID)
		}

		stateNode.SetAttr("backColor", formatMsiColor(s.BackColor, "navy"))
		stateNode.SetAttr("borderColor", "navy")
		stateNode.SetAttr("patternColor", "white")
		stateNode.SetAttr("patternStyle", "none")
		stateNode.SetAttr("blink", "false")
		stateNode.SetAttr("endColor", "white")
		stateNode.SetAttr("gradientStop", "50")
		stateNode.SetAttr("gradientDirection", "gradientDirectionHorizontal")
		stateNode.SetAttr("gradientShadingStyle", "gradientHorizontalFromRight")

		captionNode := newXNode("caption")
		font := defaultMsiFont()
		if s.Font != nil {
			font = *s.Font
		}
		captionNode.SetAttr("fontFamily", font.FontName)
		captionNode.SetAttr("fontSize", formatMsiSize(font.SizePt))
		captionNode.SetAttr("bold", boolStr(font.Bold))
		captionNode.SetAttr("italic", boolStr(font.Italic))
		captionNode.SetAttr("underline", boolStr(font.Underline))
		captionNode.SetAttr("strikethrough", boolStr(font.Strikethrough))
		captionNode.SetAttr("caption", s.Text)
		captionNode.SetAttr("color", formatMsiColor(s.FontColor, "white"))
		captionNode.SetAttr("backColor", "navy")
		captionNode.SetAttr("backStyle", "transparent")
		captionNode.SetAttr("alignment", s.Alignment)
		captionNode.SetAttr("wordWrap", "true")
		captionNode.SetAttr("blink", "false")
		stateNode.Children = append(stateNode.Children, captionNode)

		imageNode := newXNode("imageSettings")
		imageNode.SetAttr("imageName", "")
		imageNode.SetAttr("alignment", "middleCenter")
		imageNode.SetAttr("backStyle", "transparent")
		imageNode.SetAttr("color", "white")
		imageNode.SetAttr("backColor", "navy")
		imageNode.SetAttr("scaled", "false")
		imageNode.SetAttr("blink", "false")
		stateNode.Children = append(stateNode.Children, imageNode)

		statesNode.Children = append(statesNode.Children, stateNode)
	}

	// Re-attach the captured <animations> node as-is: SE and ME share
	// the exact same animateVisibility shape, so this is a structural
	// carry-over, not a conversion. Order matches real ME exports:
	// <states>, then <animations>, then <connections>.
	if animationsNode != nil {
		node.Children = append(node.Children, animationsNode)
	}

	// ME's own Connections tab has a single connection point named
	// "Indicator" (see screenshot) - this is the live runtime tag that
	// drives which state displays, and is the direct equivalent of SE's
	// "IndicatorTag" connection (not "CurrentState", which - per the
	// guide's currentstate blob-field writeup - only tracks which row was
	// highlighted in the SE property EDITOR at save time, a design-time
	// detail with no ME equivalent and nothing to convert). All of SE's
	// other 10 connection points (u_States, Border, CurrentState,
	// StateAlignment, StateBackColor, StateCount, StateFont,
	// StateFontColor, StateIndValue, StateText) have no ME connection
	// equivalent because ME made those design-time-static attributes on
	// <state>/<caption> instead of live tag bindings - they're
	// intentionally dropped, not lost.
	if len(indicatorTagExpr) > 0 {
		meConnectionsNode := newXNode("connections")
		indicatorConnectionNode := newXNode("connection")
		indicatorConnectionNode.SetAttr("name", "Indicator")
		indicatorConnectionNode.SetAttr("expression", indicatorTagExpr)
		meConnectionsNode.Children = append(meConnectionsNode.Children, indicatorConnectionNode)
		node.Children = append(node.Children, meConnectionsNode)
	} else {
		c.Warnings = append(c.Warnings, fmt.Sprintf("<multistateIndicator name=\"%s\">: SE connection \"IndicatorTag\" missing/empty - \"Indicator\" not created, add tag manually.", name))
	}

	c.Warnings = append(c.Warnings, fmt.Sprintf("<multistateIndicator name=\"%s\">: values decoded from reverse-engineered binary format - verify visually in ME editor.", name))
	return true
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// formatMsiColor formats an OLE_COLOR-derived color as a "#RRGGBB" hex
// string (ME12 confirmed to accept raw hex strings for these attributes -
// see the guide's second ME12 sample section). System colors (high bit
// set) are resolved to their real, standard Windows RGB values via
// resolveSystemColor rather than discarded - discarding them was a bug:
// COLOR_BTNFACE (the common default back color) was silently replaced
// with the literal fallback (e.g. "navy") instead of the light gray it
// actually renders as. `fallbackNamed` is now only used for a genuinely
// unrecognized system-color index.
func formatMsiColor(c *msiColor, fallbackNamed string) string {
	if c == nil {
		return fallbackNamed
	}
	if c.IsSystemColor {
		if resolved, ok := resolveSystemColor(c.SystemColorIndex); ok {
			return resolved
		}
		return fallbackNamed
	}
	return fmt.Sprintf("#%02X%02X%02X", c.R, c.G, c.B)
}

// resolveSystemColor holds standard Windows system color RGB values,
// keyed by the low byte of the OLE_COLOR system-color sentinel
// (0x8000000X). Values are the default Windows 10/11 theme RGBs. Only the
// indices actually seen in this control's exports are mapped; add more
// here if new indices turn up in other displays.
func resolveSystemColor(index int) (string, bool) {
	switch index {
	case 0x0F: // COLOR_BTNFACE / COLOR_3DFACE - standard button-face light gray
		return "#F0F0F0", true
	case 0x12: // COLOR_BTNTEXT - standard button text, black
		return "#000000", true
	case 0x05: // COLOR_WINDOW - window background, white
		return "#FFFFFF", true
	case 0x08: // COLOR_WINDOWTEXT - window text, black
		return "#000000", true
	default:
		return "", false
	}
}

// formatMsiSize renders ME's fontSize as a plain point-size (no *10000
// scaling, unlike SE's on-wire IFont struct - confirmed in the guide's
// second ME12 sample section). Rounded to the nearest whole point since
// ME's own samples only ever show integer fontSize values; SE's IFont
// struct itself uses a fixed-point value with 4 implied decimal digits,
// so this is a deliberate, minor lossy step.
func formatMsiSize(sizePt float64) string {
	return strconv.Itoa(int(roundAwayFromZero(sizePt)))
}

// roundAwayFromZero mirrors C#'s Math.Round(..., MidpointRounding.AwayFromZero).
func roundAwayFromZero(v float64) float64 {
	if v >= 0 {
		return float64(int64(v + 0.5))
	}
	return float64(int64(v - 0.5))
}

// parseMsiEntries walks the flat entry sequence, returning name -> raw
// payload bytes (everything after the UTF-16LE name, i.e. value + tag1 +
// tag2 + checksum, or just value for the one truncated tail entry).
// Returns ok=false if the blob doesn't even have a valid 16-byte outer
// header, or if a panic-prone bounds condition would otherwise occur (the
// C# original catches IndexOutOfRangeException/ArgumentOutOfRangeException
// around both parsing and interpretation; the Go port instead structures
// the loop so those conditions are checked explicitly up front).
func parseMsiEntries(blob []byte) (map[string][]byte, bool) {
	if len(blob) < 16 {
		return nil, false
	}

	result := make(map[string][]byte)
	pos := 16
	for pos+4 <= len(blob) {
		entryLen := int(int32(binary.LittleEndian.Uint32(blob[pos : pos+4])))

		var entryBytes []byte
		var truncated bool
		if entryLen < 4 || pos+entryLen > len(blob) {
			// Malformed/negative length, or would overrun the blob: treat
			// as the final, truncated entry and salvage what's left
			// rather than aborting the whole parse. Confirmed to happen
			// for exactly the last entry in every sample seen.
			entryBytes = append([]byte(nil), blob[pos:]...)
			truncated = true
		} else {
			entryBytes = append([]byte(nil), blob[pos:pos+entryLen]...)
			truncated = false
		}

		name, nameEndOffset := readMsiEntryName(entryBytes, 4)
		if len(name) > 0 {
			rest := append([]byte(nil), entryBytes[nameEndOffset:]...)
			result[name] = rest
		}

		if truncated {
			break
		}
		pos += entryLen
	}

	return result, true
}

// readMsiEntryName decodes entry names, which are UTF-16LE with no
// explicit length prefix; the name just runs until a non-ASCII-printable
// UTF-16 code unit is hit (the start of the value payload). Returns the
// decoded name and the byte offset immediately after it (relative to the
// start of `data`).
func readMsiEntryName(data []byte, start int) (string, int) {
	var units []uint16
	i := start
	for i+1 < len(data) {
		val := int(data[i]) | (int(data[i+1]) << 8)
		if val >= 32 && val < 127 {
			units = append(units, uint16(val))
			i += 2
		} else {
			break
		}
	}
	return string(utf16.Decode(units)), i
}

// decodeMsiColor decodes an OLE_COLOR-shaped 4-byte payload. It reports
// ok=false if rest is too short to contain the color, mirroring the C#
// original's BitConverter.ToUInt32(rest, 0), which throws ArgumentException
// on a too-short array; interpretMsiEntries treats that the same as any
// other malformed-entry failure (abort the whole decode, fall back to
// opaque activeX passthrough) rather than fabricating a zero-value color,
// which would silently substitute black/non-system for a color that was
// never actually decoded.
func decodeMsiColor(rest []byte) (*msiColor, bool) {
	if len(rest) < 4 {
		return nil, false
	}
	raw := binary.LittleEndian.Uint32(rest[0:4])
	isSystem := raw&0x80000000 != 0
	c := &msiColor{
		IsSystemColor: isSystem,
		R:             byte(raw & 0xFF),
		G:             byte((raw >> 8) & 0xFF),
		B:             byte((raw >> 16) & 0xFF),
	}
	if isSystem {
		c.SystemColorIndex = int(raw & 0xFF)
	}
	return c, true
}

// decodeMsiFont decodes a statefont{N}/statefont-1 entry's payload
// (starts at the CLSID_StdFont GUID). Returns nil if the GUID doesn't
// match what every sample has shown so far, or if the stream is too
// short to contain the fixed-layout fields - both treated as "this blob
// doesn't look like the format we understand" rather than guessed at.
func decodeMsiFont(rest []byte) *msiFont {
	if len(rest) < 16+11 {
		return nil
	}
	for i := 0; i < 16; i++ {
		if rest[i] != stdFontGUID[i] {
			return nil
		}
	}

	p := 16
	styleFlags := rest[p+3]
	weight := binary.LittleEndian.Uint16(rest[p+4 : p+6])
	sizeRaw := binary.LittleEndian.Uint32(rest[p+6 : p+10])
	nameLen := int(rest[p+10])
	nameStart := p + 11
	if nameStart+nameLen > len(rest) {
		return nil
	}
	fontName := string(rest[nameStart : nameStart+nameLen])

	return &msiFont{
		FontName:      fontName,
		SizePt:        float64(sizeRaw) / 10000.0,
		Bold:          weight >= 700,
		Italic:        styleFlags&0x02 != 0,
		Underline:     styleFlags&0x04 != 0,
		Strikethrough: styleFlags&0x08 != 0,
	}
}

// stateEntrySuffixRegex matches the state-index suffix on every per-state
// property EXCEPT StateText (which has no prefix at all - see the
// guide's StateText naming quirk, handled separately below).
var stateEntrySuffixRegex = regexp.MustCompile(`^(?:statefont|statefontcolor|statebackcolor|statealignment)(-?\d+)$`)

// discoverStateIDs discovers which state IDs actually exist in this blob
// by scanning the parsed entry names, instead of trusting a fixed
// 5-element list (-1,0,1,2,3) or the "statecount" entry's value.
//
// Deliberately NOT using "statecount": readMsiEntryName has no explicit
// name-length field, so it stops at the first UTF-16 code unit outside
// printable ASCII. When statecount's value is e.g. 100 (0x64 00 00 00
// LE), the first 16-bit word is 0x0064 = ASCII 'd', which the greedy name
// reader swallows as part of the name - so the entry ends up stored under
// the key "statecountd", not "statecount". This doesn't corrupt anything
// else (entry boundaries come from entry_len, not name parsing), but it
// does mean a direct entries["statecount"] lookup silently misses for
// state counts whose value happens to collide with a printable low byte.
// Scanning the per-state property names themselves sidesteps this
// entirely and works for any state count.
func discoverStateIDs(entries map[string][]byte) []string {
	ids := map[int]struct{}{-1: {}}
	for key := range entries {
		if m := stateEntrySuffixRegex.FindStringSubmatch(key); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil {
				ids[n] = struct{}{}
				continue
			}
		}

		// Bare numeric entry names ("-1","0","1",...) are StateText
		// entries. Guard with a round-trip check so we never mistake some
		// other, unrelated property name for a state ID.
		if n, err := strconv.Atoi(key); err == nil && strconv.Itoa(n) == key {
			ids[n] = struct{}{}
		}
	}

	sorted := make([]int, 0, len(ids))
	for n := range ids {
		if n != -1 {
			sorted = append(sorted, n)
		}
	}
	sort.Ints(sorted)

	ordered := make([]string, 0, len(sorted)+1)
	ordered = append(ordered, "-1")
	for _, n := range sorted {
		ordered = append(ordered, strconv.Itoa(n))
	}
	return ordered
}

func interpretMsiEntries(entries map[string][]byte) (*msiData, bool) {
	data := &msiData{Border: 1, States: map[string]msiState{}}

	if borderRaw, ok := entries["border"]; ok {
		if len(borderRaw) < 4 {
			return nil, false
		}
		data.Border = int(int32(binary.LittleEndian.Uint32(borderRaw[0:4])))
	}

	if curRaw, ok := entries["currentstate"]; ok {
		if len(curRaw) < 2 {
			return nil, false
		}
		data.CurrentState = int16(binary.LittleEndian.Uint16(curRaw[0:2]))
	}

	for _, stateID := range discoverStateIDs(entries) {
		s := newMsiState()

		if textRaw, ok := entries[stateID]; ok {
			if len(textRaw) < 4 {
				return nil, false
			}
			charCount := int(int32(binary.LittleEndian.Uint32(textRaw[0:4])))
			byteLen := charCount * 2
			if charCount < 0 || 4+byteLen > len(textRaw) {
				return nil, false
			}
			units := make([]uint16, charCount)
			for i := 0; i < charCount; i++ {
				off := 4 + i*2
				units[i] = binary.LittleEndian.Uint16(textRaw[off : off+2])
			}
			s.Text = string(utf16.Decode(units))
		}

		alignKey := "statealignment" + stateID
		if alignRaw, ok := entries[alignKey]; ok {
			// StateError ("-1") serializes as a 4-byte int; numbered
			// states (0-3) serialize as a 2-byte int - confirmed
			// structural difference, not value-dependent (see guide).
			var val int
			if stateID == "-1" {
				if len(alignRaw) < 4 {
					return nil, false
				}
				val = int(int32(binary.LittleEndian.Uint32(alignRaw[0:4])))
			} else {
				if len(alignRaw) < 2 {
					return nil, false
				}
				val = int(int16(binary.LittleEndian.Uint16(alignRaw[0:2])))
			}
			if val >= 0 && val < 9 {
				s.Alignment = alignmentNames[val]
			} else {
				s.Alignment = "middleCenter"
			}
		}

		fontColorKey := "statefontcolor" + stateID
		if fcRaw, ok := entries[fontColorKey]; ok {
			color, ok := decodeMsiColor(fcRaw)
			if !ok {
				return nil, false
			}
			s.FontColor = color
		}

		backColorKey := "statebackcolor" + stateID
		if bcRaw, ok := entries[backColorKey]; ok {
			color, ok := decodeMsiColor(bcRaw)
			if !ok {
				return nil, false
			}
			s.BackColor = color
		}

		fontKey := "statefont" + stateID
		if fontRaw, ok := entries[fontKey]; ok {
			s.Font = decodeMsiFont(fontRaw)
		}

		data.States[stateID] = s
	}

	return data, true
}
