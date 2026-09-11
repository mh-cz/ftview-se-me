package ftviewconverter

import "strings"

// ============================================================================
// Shared lookup tables (built once, not per node)
// ============================================================================

// seOnlyCommonAttrs are attributes present on (almost) every SE drawing
// object that ME simply does not support and which are safe to drop
// outright.
var seOnlyCommonAttrs = map[string]struct{}{
	"toolTipText": {}, "exposeToVba": {}, "tabIndex": {}, "pointerHighlight": {},
	"linkAnimations": {}, "linkBaseObject": {}, "linkConnections": {},
	"linkSize": {}, "linkToolTipText": {},
}

var seDisplaySettingsOnlyAttrs = []string{
	"TrackName", "TrackScreenForNavigation", "allowButtonActionOnError",
	"allowMultipleRunningCopies", "allowResizing", "beepOnPress",
	"cacheAfterDisplaying", "displayOnScreenKeyboard",
	"fieldInErrorNotSelectedFillColor", "fieldInErrorNotSelectedTextColor",
	"fieldInErrorSelectedFillColor", "fieldInErrorSelectedTextColor",
	"fieldNotSelectedFillColor", "fieldNotSelectedTextColor",
	"fieldSelectedFillColor", "fieldSelectedTextColor",
	"highlightWhenCursorPassesOver", "interactiveHighlightColor",
	"keepAtBack", "minimizeButton", "pinButton", "showLastAcquiredValue",
	"sizeToMainWindow", "systemMenu", "titleBarText", "whenResized",
}

var numericMeOnlyDropAttrs = []string{"format", "overflow", "showDigitGrouping", "decimalPlaceType"}

var fieldDisplayVisualDropAttrs = []string{
	"blink", "patternColor", "patternStyle", "description",
	"borderColor", "borderStyle", "borderUsesBackColor", "borderWidth",
	"endColor", "gradientStop", "gradientDirection", "gradientShadingStyle",
}

var stringDisplaySeOnlySizingAttrs = []string{
	"dimensionsWidth", "dimensionsHeight", "characterOffset", "charWidth", "charHeight",
}

var vbaOnlyChildTags = []string{"vbaProject", "vbaItem", "vbaCode", "encryptedData", "eSignature"}

var seNoDirectMeEquivalentTags = map[string]struct{}{
	"displayKeys": {}, "ability": {}, "confirm": {}, "eSignature": {},
	"tagLabel": {}, "arrow": {}, "encryptedData": {}, "vbaProject": {}, "vbaItem": {},
	"vbaCode": {}, "animateHeight": {}, "animateHorizontalPosition": {},
	"animateTouch": {}, "animateVerticalPosition": {}, "animateVerticalSlider": {},
	"animateWidth": {}, "readFromTagExpressionRange": {}, "parameter": {}, "parameters": {},
	"navigationButton": {},
}

var meNoDirectSeEquivalentTags = map[string]struct{}{
	"gotoButton": {}, "shutdownButton": {}, "closeButton": {},
	"acknowledgeAlarmButton": {}, "acknowledgeAllAlarmsButton": {}, "silenceAlarmsButton": {},
	"resetAlarmStatusButton": {}, "alarmList": {}, "alarmStatusList": {},
	"alarmStatusModeButton": {}, "clearAlarmHistoryButton": {}, "diagnosticsList": {},
	"diagnosticsClearAllButton": {}, "diagnosticsClearButton": {}, "printAlarmStatus": {},
	"polyPolygon": {}, "innerPolygon": {}, "animateRotation": {},
	"loginButton": {}, "logoutButton": {},
	"passwordButton": {}, "languageSwitchButton": {}, "macroButton": {}, "returnToButton": {},
	"displayPrintButton": {}, "trendNextPenButton": {}, "trendPauseButton": {},
	"defaultExpressionRange": {}, "image": {}, "polyline": {},
}

var seCommonDefaults = []kv{
	{"toolTipText", ""},
	{"exposeToVba", "notExposed"},
}

// fieldDisplaySeToMeDefaults is shared by numericDisplay and stringDisplay
// SE->ME conversion (the original GDScript literally duplicated this
// dictionary in both functions; it's the same set of ME-only visual
// attributes).
var fieldDisplaySeToMeDefaults = []kv{
	{"blink", "false"},
	{"patternColor", "white"},
	{"patternStyle", "none"},
	{"description", ""},
	{"borderStyle", "none"},
	{"borderUsesBackColor", "true"},
	{"borderWidth", "1"},
	{"endColor", "white"},
	{"gradientStop", "50"},
	{"gradientDirection", "gradientDirectionHorizontal"},
	{"gradientShadingStyle", "gradientHorizontalFromRight"},
}

var numericMeToSeExtraDefaults = []kv{
	{"overflow", "fillWithAsterisks"},
	{"showDigitGrouping", "false"},
	{"decimalPlaceType", "fixed"},
}

var displaySettingsMeToSeDefaults = []kv{
	{"titleBarText", ""},
	{"allowMultipleRunningCopies", "false"},
	{"cacheAfterDisplaying", "false"},
	{"systemMenu", "true"},
	{"minimizeButton", "true"},
	{"sizeToMainWindow", "false"},
	{"pinButton", "false"},
	{"showLastAcquiredValue", "true"},
	{"TrackScreenForNavigation", "true"},
	{"TrackName", ""},
	{"allowResizing", "false"},
	{"whenResized", "scale"},
	{"beepOnPress", "false"},
	{"highlightWhenCursorPassesOver", "true"},
	{"interactiveHighlightColor", "black"},
	{"displayOnScreenKeyboard", "false"},
	{"allowButtonActionOnError", "true"},
	{"fieldNotSelectedTextColor", "black"},
	{"fieldNotSelectedFillColor", "white"},
	{"fieldSelectedTextColor", "black"},
	{"fieldSelectedFillColor", "white"},
	{"fieldInErrorNotSelectedTextColor", "black"},
	{"fieldInErrorNotSelectedFillColor", "red"},
	{"fieldInErrorSelectedTextColor", "white"},
	{"fieldInErrorSelectedFillColor", "red"},
}

// kv is an ordered key/value pair, used in place of C#'s Dictionary
// literals above so default-application order matches the source
// (Go maps have no defined iteration order).
type kv struct {
	Key, Value string
}

func ensureDefaults(node *XNode, defaults []kv) {
	for _, d := range defaults {
		if !node.Has(d.Key) {
			node.SetAttr(d.Key, d.Value)
		}
	}
}

func stripSeOnlyCommonAttrs(node *XNode) {
	for attr := range seOnlyCommonAttrs {
		node.RemoveAttr(attr)
	}
}

// addSeCommonDefaults is only meaningful on visual/drawing objects (they
// all carry `name` in both editions); it's a no-op on container/meta
// nodes like <gfx> or <caption>-only wrappers.
func addSeCommonDefaults(node *XNode) {
	if !node.Has("name") {
		return
	}
	ensureDefaults(node, seCommonDefaults)
}

func justificationToAlignment(justification string) string {
	switch justification {
	case "left":
		return "middleLeft"
	case "right":
		return "middleRight"
	case "center":
		return "middleCenter"
	default:
		return "middleCenter"
	}
}

func alignmentToJustification(alignment string) string {
	lower := strings.ToLower(alignment)
	if strings.Contains(lower, "left") {
		return "left"
	}
	if strings.Contains(lower, "right") {
		return "right"
	}
	return "center"
}

// containsString reports whether slice contains s.
func containsString(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

// isGroupChild reports whether parent is a <group> element. FactoryTalk
// View ME's importer rejects a `wallpaper` attribute on any object that
// is a direct child of a group (confirmed by a real import log:
// "The wallpaper cannot be set on 'NumericInput1', as it is the child of
// a group."), presumably because wallpaper is inherited from the group
// itself rather than settable per-child. Rebuild functions that always
// wrote a `wallpaper` attribute must skip it when their new parent is a
// group.
func isGroupChild(parent *XNode) bool {
	return parent != nil && parent.Tag == "group"
}
