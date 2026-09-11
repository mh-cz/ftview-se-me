package ftviewconverter

import (
	"fmt"
	"math"
)

// ============================================================================
// ME -> SE
// ============================================================================

// removalSentinelTag is used the same way as the C# original:
// convertNodeMeToSe (unlike its SE->ME counterpart) recurses into children
// unconditionally from the caller's own body rather than a filtering
// loop, so a node can't remove itself from its parent's child list at
// this point in the walk. Marking it with this sentinel tag lets the
// parent's recursion step filter it out afterward; see the pruning pass
// at the end of convertNodeMeToSe.
const removalSentinelTag = "__ftview_removed__"

func markForRemoval(node *XNode) {
	node.Tag = removalSentinelTag
}

func (c *Converter) convertNodeMeToSe(node, parent *XNode) {
	switch node.Tag {
	case "gfx":
		node.SetAttr("xsi:noNamespaceSchemaLocation", "Gfx-SE12.xsd")
	case "displaySettings":
		c.displaySettingsMeToSe(node)
	case "numericDisplay":
		c.numericDisplayMeToSe(node)
	case "stringDisplay":
		c.stringDisplayMeToSe(node)
	case "imageSettings":
		node.RenameAttr("imageName", "imageFile")
		if node.Has("imageFile") && node.GetAttr("imageFile") != "" {
			node.SetAttr("imageReference", "importFile")
		} else {
			node.SetAttr("imageReference", "noImage")
		}
	case "numericInputCursorPoint":
		c.numericInputCursorPointMeToSe(node, parent)
	case "numericInputEnable":
		c.numericInputEnableMeToSe(node)
	case "stringInputEnable":
		c.stringInputEnableMeToSe(node)
	case "activeX":
		c.activeXCrossEdition(node, MeToSe)
	case "multistateIndicator":
		c.multistateIndicatorMeToSe(node)
	default:
		if _, ok := meNoDirectSeEquivalentTags[node.Tag]; ok {
			// This ME-only element type has no special-cased SE rebuild
			// rule above (unlike numericInputEnable/stringInputEnable,
			// which are actively markForRemoval'd). It's passed through
			// as-is with only generic SE defaults added, since SE has no
			// native object shaped like it - verify manually that the
			// passthrough attributes are meaningful in SE.
			c.Warnings = append(c.Warnings, fmt.Sprintf("<%s> has no SE equivalent - kept unchanged, verify it makes sense in SE.", node.Tag))
		}
		addSeCommonDefaults(node)
	}

	if node.Tag == removalSentinelTag {
		return // Marked for removal by its own case above; no need to convert its children.
	}

	kept := make([]*XNode, 0, len(node.Children))
	for _, child := range node.Children {
		c.convertNodeMeToSe(child, node)
		if child.Tag != removalSentinelTag {
			kept = append(kept, child)
		}
	}
	node.Children = kept
}

// numericInputCursorPointMeToSe converts ME <numericInputCursorPoint> ->
// SE <numericInput>. Mirror of numericInputSeToMe: same field concept,
// different attribute set. ME's on-screen keypad, handshake timing, and
// min/max-on-the-field attributes have no SE counterpart and are
// dropped.
func (c *Converter) numericInputCursorPointMeToSe(node *XNode, parent *XNode) {
	name := node.GetAttr("name")
	height := node.GetAttr("height")
	width := node.GetAttr("width")
	left := node.GetAttr("left")
	top := node.GetAttr("top")
	visible := node.GetAttr("visible", "true")
	fontFamily := node.GetAttr("fontFamily", "Arial")
	fontSize := node.GetAttr("fontSize", "10")
	bold := node.GetAttr("bold", "false")
	italic := node.GetAttr("italic", "false")
	underline := node.GetAttr("underline", "false")
	strikethrough := node.GetAttr("strikethrough", "false")
	numberOfDigits := node.GetAttr("numberOfDigits", "6")
	decimalPlaces := node.GetAttr("decimalPlaces", "0")
	alignment := node.GetAttr("alignment", "middleCenter")

	node.ClearAttrs()
	node.Tag = "numericInput"

	node.SetAttr("name", name)
	if height != "" {
		node.SetAttr("height", height)
	}
	if width != "" {
		node.SetAttr("width", width)
	}
	if left != "" {
		node.SetAttr("left", left)
	}
	if top != "" {
		node.SetAttr("top", top)
	}
	node.SetAttr("visible", visible)
	if !isGroupChild(parent) {
		node.SetAttr("wallpaper", "false")
	}
	node.SetAttr("toolTipText", "")
	node.SetAttr("exposeToVba", "notExposed")
	node.SetAttr("isReferenceObject", "false")
	node.SetAttr("fontFamily", fontFamily)
	node.SetAttr("fontSize", fontSize)
	node.SetAttr("bold", bold)
	node.SetAttr("italic", italic)
	node.SetAttr("underline", underline)
	node.SetAttr("strikethrough", strikethrough)
	node.SetAttr("fieldLength", numberOfDigits)
	node.SetAttr("justification", alignmentToJustification(alignment))
	node.SetAttr("enabledWhenExpressionIsTrue", "true")
	node.SetAttr("tabIndex", "1")
	node.SetAttr("continuouslyUpdate", "true")
	node.SetAttr("discardInputOnFocusLost", "false")
	node.SetAttr("defaultData", "")
	node.SetAttr("decimalPlaces", decimalPlaces)
	node.SetAttr("format", "decimal")
	node.SetAttr("overflow", "showExponent")
	node.SetAttr("leadingCharacter", "blanks")
	node.SetAttr("inputSecurityCode", "*")
	node.SetAttr("displayOnScreenKeypad", "false")
	node.SetAttr("keypadCustomCaption", "")
	node.SetAttr("decimalPlaceType", "fixed")

	c.Warnings = append(c.Warnings, fmt.Sprintf("<numericInputCursorPoint name=\"%s\"> -> <numericInput>: keypad/handshake attributes removed.", name))
}

// numericInputEnableMeToSe and stringInputEnableMeToSe handle ME
// <numericInputEnable>/<stringInputEnable>, which are button-shaped
// objects (rectangle + caption + imageSettings) that open an on-screen
// keypad popup when pressed. SE's <numericInput>/<stringInput> are plain
// inline entry fields with no caption/button chrome at all. Rebuilding
// one as the other would silently throw away the visual button and its
// caption/image, which is a worse outcome than an honest drop, so both
// are dropped with a warning rather than guessed at.
func (c *Converter) numericInputEnableMeToSe(node *XNode) {
	c.Warnings = append(c.Warnings, fmt.Sprintf("<numericInputEnable name=\"%s\"> removed (button, not a field) - add SE numericInput manually.", node.GetAttr("name")))
	markForRemoval(node)
}

func (c *Converter) stringInputEnableMeToSe(node *XNode) {
	c.Warnings = append(c.Warnings, fmt.Sprintf("<stringInputEnable name=\"%s\"> removed (button, not a field) - add SE stringInput manually.", node.GetAttr("name")))
	markForRemoval(node)
}

func (c *Converter) displaySettingsMeToSe(node *XNode) {
	posX := node.GetAttr("positionX")
	posY := node.GetAttr("positionY")
	node.RemoveAttr("positionX")
	node.RemoveAttr("positionY")
	if posX != "" || posY != "" {
		node.SetAttr("position", "specifyPositionInPixels")
		if posX != "" {
			node.SetAttr("positionX", posX)
		} else {
			node.SetAttr("positionX", "0")
		}
		if posY != "" {
			node.SetAttr("positionY", posY)
		} else {
			node.SetAttr("positionY", "0")
		}
	} else {
		node.SetAttr("position", "useCurrentPosition")
	}

	node.RemoveAttr("displayNumber")

	node.RenameAttr("startupMacro", "startupCommand")
	node.RenameAttr("shutdownMacro", "shutdownCommand")
	node.RemoveAttr("disableInitialInputFocus")

	ensureDefaults(node, displaySettingsMeToSeDefaults)
}

func (c *Converter) numericDisplayMeToSe(node *XNode) {
	alignment := node.GetAttr("alignment", "middleRight")
	node.RemoveAttr("alignment")
	node.SetAttr("justification", alignmentToJustification(alignment))

	node.RenameAttr("numberOfDigits", "fieldLength")
	if !node.Has("fieldLength") {
		node.SetAttr("fieldLength", "5")
	}

	node.RenameAttr("fillLeftWith", "leadingCharacter")
	if node.Has("leadingCharacter") && node.GetAttr("leadingCharacter") == "spaces" {
		node.SetAttr("leadingCharacter", "blanks")
	} else if !node.Has("leadingCharacter") {
		node.SetAttr("leadingCharacter", "blanks")
	}

	dp := node.GetAttr("decimalPlaces", "0")
	if !node.Has("format") {
		if dp != "0" {
			node.SetAttr("format", "floatingPoint")
		} else {
			node.SetAttr("format", "decimal")
		}
	}
	ensureDefaults(node, numericMeToSeExtraDefaults)

	for _, a := range fieldDisplayVisualDropAttrs {
		node.RemoveAttr(a)
	}

	addSeCommonDefaults(node)
}

func (c *Converter) stringDisplayMeToSe(node *XNode) {
	alignment := node.GetAttr("alignment", "middleLeft")
	node.RemoveAttr("alignment")
	node.SetAttr("justification", alignmentToJustification(alignment))

	fontSize := parseInvariantDouble(node.GetAttr("fontSize", "10"), 10.0)
	widthPx := parseInvariantDouble(node.GetAttr("width", "100"), 100.0)
	heightPx := parseInvariantDouble(node.GetAttr("height", "20"), 20.0)
	approxCharW := math.Max(fontSize*0.6, 1.0)
	approxLineH := math.Max(fontSize*1.3, 1.0)
	node.SetAttr("dimensionsWidth", formatInvariantInt(maxInt(1, int(widthPx/approxCharW))))
	node.SetAttr("dimensionsHeight", formatInvariantInt(maxInt(1, int(heightPx/approxLineH))))
	node.SetAttr("characterOffset", "0")
	c.Warnings = append(c.Warnings, fmt.Sprintf("<stringDisplay name=\"%s\">: dimensionsWidth/dimensionsHeight estimated from pixel size - check wrapping/overflow.", node.GetAttr("name")))

	for _, a := range fieldDisplayVisualDropAttrs {
		node.RemoveAttr(a)
	}

	addSeCommonDefaults(node)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
