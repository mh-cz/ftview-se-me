package ftviewconverter

import (
	"fmt"
	"math"
	"strings"
)

// ============================================================================
// SE -> ME
// ============================================================================

// describeRemovedVbaContent renders identifying detail about a VBA-only
// subtree that's about to be dropped (vbaProject/vbaItem/vbaCode/
// encryptedData/eSignature - see vbaOnlyChildTags), for appending to the
// removal warning. The VBA source itself is embedded as a base64-encoded,
// Microsoft-encrypted binary stream inside <encryptedData>'s CDATA body -
// not plaintext code - so dumping it into a warning would be useless
// noise (and potentially huge for a real project). What IS useful and
// compact is each <encryptedData> element's `hash` attribute, which
// identifies a specific VBA item/module; every one found anywhere in the
// dropped subtree is listed. Returns "" (no parenthetical) if the
// subtree contains no encryptedData with a hash.
func describeRemovedVbaContent(node *XNode) string {
	var hashes []string
	var walk func(n *XNode)
	walk = func(n *XNode) {
		if n.Tag == "encryptedData" {
			if h := n.GetAttr("hash", ""); h != "" {
				hashes = append(hashes, h)
			}
		}
		for _, ch := range n.Children {
			walk(ch)
		}
	}
	walk(node)

	if len(hashes) == 0 {
		return ""
	}
	return fmt.Sprintf(" Encrypted VBA data removed (hash: %s).", strings.Join(hashes, ", "))
}

// describeRemovedExpression renders the tag expression(s) actually
// present on a subtree that's about to be dropped whole (see
// seNoDirectMeEquivalentTags), for appending to the removal warning -
// e.g. <ability expression="...">, <animateHeight expression="...">.
// Not every dropped tag carries one (many, like <displayKeys> or
// <tagLabel>, don't), so this only adds detail where there's something
// concrete to show, rather than padding every warning with an empty
// clause. Collects from the node itself and its descendants, since a
// container tag's own expression sometimes lives on a nested child.
func describeRemovedExpression(node *XNode) string {
	var exprs []string
	var walk func(n *XNode)
	walk = func(n *XNode) {
		if e := n.GetAttr("expression", ""); e != "" {
			exprs = append(exprs, e)
		}
		for _, ch := range n.Children {
			walk(ch)
		}
	}
	walk(node)

	if len(exprs) == 0 {
		return ""
	}
	if len(exprs) == 1 {
		return fmt.Sprintf(" Removed expression: %q.", exprs[0])
	}
	quoted := make([]string, len(exprs))
	for i, e := range exprs {
		quoted[i] = fmt.Sprintf("%q", e)
	}
	return fmt.Sprintf(" Removed expressions: %s.", strings.Join(quoted, ", "))
}

func (c *Converter) convertNodeSeToMe(node, parent *XNode) {
	switch node.Tag {
	case "gfx":
		node.SetAttr("xsi:noNamespaceSchemaLocation", "Gfx-ME12.xsd")
	case "displaySettings":
		c.displaySettingsSeToMe(node)
	case "numericDisplay":
		c.numericDisplaySeToMe(node)
	case "stringDisplay":
		c.stringDisplaySeToMe(node)
	case "imageSettings":
		node.RenameAttr("imageFile", "imageName")
		node.RemoveAttr("imageReference")
	case "numericInput":
		c.numericInputSeToMe(node, parent)
	case "activeX":
		c.activeXCrossEdition(node, SeToMe)
	default:
		if _, ok := seNoDirectMeEquivalentTags[node.Tag]; ok {
			// Note: this is the node currently being *visited* (not a
			// child being dropped from its parent - that's the separate
			// whole-subtree-drop path below at seNoDirectMeEquivalentTags
			// for children). The node itself is kept; only its SE-only
			// attributes get stripped, so the warning says so rather than
			// implying removal.
			c.Warnings = append(c.Warnings, fmt.Sprintf("<%s> has no ME equivalent - kept, but SE-specific attributes removed.", node.Tag))
		}
		stripSeOnlyCommonAttrs(node)
	}

	kept := make([]*XNode, 0, len(node.Children))
	for _, child := range node.Children {
		if containsString(vbaOnlyChildTags, child.Tag) {
			c.Warnings = append(c.Warnings, fmt.Sprintf("<%s> removed - VBA not supported in ME.%s", child.Tag, describeRemovedVbaContent(child)))
			continue
		}

		if child.Tag == "button" {
			if c.convertButtonSeToMe(child, node) {
				kept = append(kept, child)
			}
			continue
		}

		if child.Tag == "stringInput" {
			c.stringInputSeToMe(child, node)
			kept = append(kept, child)
			continue
		}

		// Whole-subtree drops: these SE-only container objects (and their
		// internal state children like <active>/<inactive>/<up>/<down>/
		// <command>) have no ME equivalent at all, so we drop the entire
		// child subtree here rather than letting the generic default case
		// recurse into their children and silently pass properties that
		// don't exist in ME's schema.
		if _, ok := seNoDirectMeEquivalentTags[child.Tag]; ok {
			c.Warnings = append(c.Warnings, fmt.Sprintf("<%s name=\"%s\"> removed with its contents - no ME equivalent exists.%s", child.Tag, child.GetAttr("name", ""), describeRemovedExpression(child)))
			continue
		}

		c.convertNodeSeToMe(child, node)
		kept = append(kept, child)
	}
	node.Children = kept
}

func (c *Converter) convertButtonSeToMe(node, parent *XNode) bool {
	name := node.GetAttr("name")

	var command, upState *XNode
	for _, child := range node.Children {
		if child.Tag == "command" {
			command = child
		} else if child.Tag == "up" {
			upState = child
		}
	}

	pressAction := ""
	repeatAction := ""
	releaseAction := ""
	if command != nil {
		pressAction = command.GetAttr("pressAction")
		repeatAction = command.GetAttr("repeatAction")
		releaseAction = command.GetAttr("releaseAction")
	}

	// repeatAction fires continuously while the button is held down.
	// ME's button objects have no equivalent "repeat while held" firing
	// mode at all, so if repeatAction actually does anything, there is
	// no faithful ME equivalent for the button regardless of what press/
	// release do.
	if trimSpace(repeatAction) != "" {
		c.Warnings = append(c.Warnings, fmt.Sprintf("button \"%s\" removed - repeatAction has no ME equivalent.", name))
		return false
	}

	// Press and release are evaluated together as one pool of commands:
	// SE fires pressAction on press and releaseAction on release, but ME
	// gotoButton/closeButton only fire once, on press. A button that
	// e.g. runs "Display X" on press and "Abort Y" on release is, in
	// practice, still just navigation - the display swap on press
	// already closes the current screen, making the release-time Abort
	// redundant. So: gather Display/Abort commands from press and
	// release combined, and let a Display target win over a bare Abort
	// wherever it's found ("goto always beats abort").
	targetDisplay, hasAbort, ok := parseDisplayAndAbort(pressAction, releaseAction)
	if ok {
		if targetDisplay != "" {
			rebuildAsGotoButton(node, name, targetDisplay, upState, parent)
			return true
		}
		if hasAbort {
			rebuildAsCloseButton(node, name, upState, parent)
			return true
		}
	}

	// A pure login or logout button converts to ME's native
	// <loginButton>/<logoutButton> element rather than a macroButton -
	// these are first-class ME objects, not something that needs a
	// hand-written macro. SE expresses "Logoff" as the bare command of
	// that name, and expresses "log in" by launching the RsvLogin helper
	// via AppStart (confirmed against a real project: "AppStart RsvLogin
	// /OnTop /Qwerty /HiedeHelpAbout") rather than any bare "Login"
	// keyword - see parseLoginLogoutCommand.
	switch parseLoginLogoutCommand(pressAction, releaseAction) {
	case loginCommand:
		rebuildAsLoginButton(node, name, upState, parent)
		return true
	case logoutCommand:
		rebuildAsLogoutButton(node, name, upState, parent)
		return true
	}

	// Not a Display/Abort/Login/Logout button. SE's "&Set <tag> <value>"
	// macro command and the plain "<tag> =<value>" direct-assignment
	// form both write a literal value to a tag on press - ME has no
	// single object that does that inline, but ME's macroButton (which
	// runs a named macro on press) is the standard way SE->ME
	// conversions handle this: the macro itself (created separately in
	// the ME project) does the tag write. We can't synthesize a real
	// macro here, only the button that calls one, so the button is
	// rebuilt as a macroButton. The macro name is derived from the
	// tag(s)/value(s) being written (e.g. "mcr_Pump1_set_1") so the name
	// documents what the (still-to-be-created) macro actually does; if
	// the set-command text can't be turned into a meaningful name, we
	// fall back to the button's caption text as before.
	//
	// A tag-write command is sometimes combined with other commands in
	// the same action string (e.g. "Fire_POT =1;Logoff; Abort Fire") -
	// since ME buttons fire only one action, the tag write wins and the
	// rest (Logoff, Abort, or anything else) is dropped with a warning
	// that it needs to be replicated by hand (Logoff has a native ME
	// <logoutButton> that could be wired up as a separate button;
	// Display/Abort would need a separate gotoButton/closeButton).
	if setCommands, otherCommands, ok := parseSetCommands(pressAction, releaseAction); ok && len(setCommands) > 0 {
		rebuildAsMacroButton(node, name, upState, setCommands, parent, c)
		if len(otherCommands) > 0 {
			c.Warnings = append(c.Warnings, fmt.Sprintf(
				"button \"%s\": also had command(s) %s, dropped (macroButton only runs the tag write) - add manually.",
				name, strings.Join(otherCommands, "; ")))
		}
		return true
	}

	// Nothing above matched: not a pure Display/Abort, Login/Logout, or
	// tag-write(+toggle) button. Report the actual press/release command
	// text rather than a bare "no equivalent" - that's the concrete
	// reason this button couldn't be converted, and it's exactly the
	// information a human needs to rebuild it by hand.
	c.Warnings = append(c.Warnings, fmt.Sprintf(
		"button \"%s\" removed - unrecognized command(s) (pressAction=%q, releaseAction=%q), rebuild in ME manually.",
		name, pressAction, releaseAction))
	return false
}

// parseDisplayAndAbort inspects an SE button's pressAction and
// releaseAction command strings together (each itself a
// semicolon-separated command list, e.g. "Abort new_auto;Display ord")
// and reports:
//   - targetDisplay: the argument of the single unambiguous "Display
//     <name>" command found across both strings, if any
//   - hasAbort: whether an "Abort ..." command is present anywhere
//     across both strings
//   - ok: false if any command besides Display/Abort was found (in
//     which case the caller should give up rather than guess), or if
//     more than one distinct Display target was specified
//
// "Goto always beats abort": if a Display target is found at all, it
// takes priority (see convertButtonSeToMe) - a display swap already
// closes the current screen, so any accompanying Abort is redundant and
// safely dropped rather than treated as a conflict.
func parseDisplayAndAbort(pressAction, releaseAction string) (targetDisplay string, hasAbort bool, ok bool) {
	display := ""
	displayCount := 0
	otherCount := 0

	scan := func(action string) {
		trimmed := trimSpace(action)
		if trimmed == "" {
			return
		}
		for _, part := range strings.Split(trimmed, ";") {
			p := trimSpace(part)
			if p == "" {
				continue
			}
			switch {
			case hasPrefix(p, "Display "):
				target := trimSpace(p[len("Display "):])
				if displayCount == 0 || target == display {
					display = target
					displayCount++
				} else {
					// A second, different Display target: ambiguous,
					// can't pick one over the other safely.
					otherCount++
				}
			case p == "Abort" || hasPrefix(p, "Abort "):
				hasAbort = true
			default:
				otherCount++
			}
		}
	}

	scan(pressAction)
	scan(releaseAction)

	if otherCount > 0 {
		return "", hasAbort, false
	}
	if displayCount == 0 && !hasAbort {
		return "", false, false
	}
	return display, hasAbort, true
}

// loginLogoutKind classifies what parseLoginLogoutCommand found.
type loginLogoutKind int

const (
	noLoginLogoutCommand loginLogoutKind = iota
	loginCommand
	logoutCommand
)

// parseLoginLogoutCommand inspects an SE button's pressAction and
// releaseAction together and reports whether the button is a pure login
// or logout button:
//
//   - logoutCommand: the only non-empty command found is the bare
//     "Logoff" command (confirmed in a real project's Fire.xml).
//   - loginCommand: the only non-empty command found launches the
//     RsvLogin helper via "AppStart RsvLogin ..." (confirmed in the same
//     project - SE has no bare "Login" keyword; logging in is done by
//     launching this external helper application, with flags like
//     /OnTop, /Qwerty, /HiedeHelpAbout controlling its window/keyboard
//     behavior. Any AppStart RsvLogin invocation is treated as a login
//     button regardless of which flags are present).
//
// Anything else (including a login/logout command mixed with other
// commands) returns noLoginLogoutCommand, leaving it to the caller's
// other parsers (parseDisplayAndAbort, parseSetCommands) to handle -
// unlike parseSetCommands, this check requires an unmixed, single-purpose
// button, since there's no ME element that is simultaneously a
// loginButton/logoutButton and something else.
func parseLoginLogoutCommand(pressAction, releaseAction string) loginLogoutKind {
	var commands []string
	collect := func(action string) {
		trimmed := trimSpace(action)
		if trimmed == "" {
			return
		}
		for _, part := range strings.Split(trimmed, ";") {
			p := trimSpace(part)
			if p != "" {
				commands = append(commands, p)
			}
		}
	}
	collect(pressAction)
	collect(releaseAction)

	if len(commands) != 1 {
		return noLoginLogoutCommand
	}

	switch {
	case commands[0] == "Logoff":
		return logoutCommand
	case hasPrefix(commands[0], "AppStart RsvLogin"):
		return loginCommand
	default:
		return noLoginLogoutCommand
	}
}

// tagWriteCommand is a single parsed tag-write instruction extracted by
// parseSetCommands: either a "&Set <tag> <value>"/"<tag> =<value>" write
// of a literal value, or a "Toggle <tag>" flip. Toggle carries no value
// (it's semantically "<tag> = not <tag>"), so IsToggle distinguishes it
// from a Set with an empty-string value; callers that build macro names
// or warning text need to say "toggle" rather than "set to <blank>".
type tagWriteCommand struct {
	Tag      string
	Value    string // unused when IsToggle is true
	IsToggle bool
}

// parseSetCommands inspects an SE button's pressAction and releaseAction
// strings together and extracts tag-write commands in any of SE's
// equivalent forms:
//
//   - "&Set <tag> <value>"  (the macro-language form)
//   - "<tag> =<value>"      (the plain direct-assignment form, e.g.
//     "Fire_POT =1")
//   - "Toggle <tag>"        (flips a boolean tag: <tag> = not <tag>)
//
// Toggle is recognized here alongside Set (rather than being left for
// otherCommands/dropped) because it's the same kind of instruction: a
// macro can contain multiple commands (unlike a button, which fires only
// one action), so "set a cause code, then flip a boolean" is exactly the
// kind of multi-step tag-write sequence a hand-written ME macro can
// still perform faithfully. See PAUSE.xml for a real example combining
// both: "&Set KNET\PROC\PAUSE_CAUSEID "201";Toggle KNET\PROC\PAUSE_OFF".
//
// Any other recognized-but-not-convertible command mixed in alongside a
// tag write - currently "Logoff" and "Abort[ <target>]" - is collected
// into otherCommands rather than aborting the whole parse: ME buttons
// fire only one action, so when a tag write is present it takes
// priority (see convertButtonSeToMe) and the rest is surfaced to the
// caller to warn about rather than silently dropped. Any command that
// isn't a tag write, Logoff, or Abort makes the whole parse bail out
// (ok=false) - same conservative policy as parseDisplayAndAbort - since
// at that point there's no more information indicating this button's
// primary purpose is a tag write.
func parseSetCommands(pressAction, releaseAction string) (setCommands []tagWriteCommand, otherCommands []string, ok bool) {
	unrecognized := 0

	scan := func(action string) {
		trimmed := trimSpace(action)
		if trimmed == "" {
			return
		}
		for _, part := range strings.Split(trimmed, ";") {
			p := trimSpace(part)
			if p == "" {
				continue
			}
			if hasPrefix(p, "&Set ") {
				rest := trimSpace(p[len("&Set "):])
				if tag, value, isAssign := splitSetArgs(rest); isAssign {
					setCommands = append(setCommands, tagWriteCommand{Tag: tag, Value: value})
					continue
				}
				unrecognized++
				continue
			}
			if hasPrefix(p, "Toggle ") {
				tag := trimSpace(p[len("Toggle "):])
				if tag != "" {
					setCommands = append(setCommands, tagWriteCommand{Tag: tag, IsToggle: true})
					continue
				}
				unrecognized++
				continue
			}
			if tag, value, isAssign := parseDirectAssignment(p); isAssign {
				setCommands = append(setCommands, tagWriteCommand{Tag: tag, Value: value})
				continue
			}
			if p == "Logoff" || p == "Abort" || hasPrefix(p, "Abort ") {
				otherCommands = append(otherCommands, p)
				continue
			}
			unrecognized++
		}
	}

	scan(pressAction)
	scan(releaseAction)

	if unrecognized > 0 || len(setCommands) == 0 {
		return nil, nil, false
	}
	return setCommands, otherCommands, true
}

// splitSetArgs splits the "<tag> <value>" argument of "&Set <tag> <value>"
// on the first space. This form is always space-separated in SE project
// files (confirmed against PAUSE.xml: &Set KNET\PROC\PAUSE_CAUSEID
// "201"), never "="-separated, so unlike parseDirectAssignment this is a
// plain split, not an "=" search.
func splitSetArgs(rest string) (tag, value string, ok bool) {
	idx := strings.IndexByte(rest, ' ')
	if idx <= 0 {
		return "", "", false
	}
	tag = trimSpace(rest[:idx])
	if tag == "" {
		return "", "", false
	}
	value = trimSpace(rest[idx+1:])
	return tag, value, true
}

// parseDirectAssignment recognizes SE's plain "<tag> =<value>" tag-write
// syntax (as opposed to the "&Set <tag> <value>" macro-language form),
// e.g. "Fire_POT =1". The tag is everything before the first "=", with
// any trailing whitespace trimmed; the value is everything after it. A
// bare "=" with no tag name on the left, or no "=" at all, is not an
// assignment.
func parseDirectAssignment(command string) (tag, value string, ok bool) {
	idx := strings.IndexByte(command, '=')
	if idx <= 0 {
		return "", "", false
	}
	tag = trimSpace(command[:idx])
	if tag == "" {
		return "", "", false
	}
	value = trimSpace(command[idx+1:])
	return tag, value, true
}

// macroNameFromCaption derives a valid ME macro identifier from a
// button's caption text, per instruction: the macro name is the
// button's own label. ME macro names can't contain the raw caption's
// whitespace/newlines/punctuation, so this collapses runs of anything
// that isn't a letter, digit, or underscore into a single underscore
// and trims the result; an empty or fully-punctuation caption falls
// back to the button's own element name so the macro reference is never
// left blank.
func macroNameFromCaption(caption, fallback string) string {
	var b strings.Builder
	lastWasSep := false
	for _, r := range caption {
		isWord := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_'
		if isWord {
			b.WriteRune(r)
			lastWasSep = false
		} else if !lastWasSep && b.Len() > 0 {
			b.WriteByte('_')
			lastWasSep = true
		}
	}
	result := strings.TrimSuffix(b.String(), "_")
	if result == "" {
		return fallback
	}
	return result
}

// sanitizeMacroNamePart collapses runs of anything that isn't a letter,
// digit, or underscore into a single underscore, and trims the result -
// the same rule macroNameFromCaption uses for caption text, reused here
// for tag names and values (which can contain dots, brackets, spaces,
// etc., e.g. "Machine1.Speed" or "-1.5").
func sanitizeMacroNamePart(s string) string {
	var b strings.Builder
	lastWasSep := false
	for _, r := range s {
		isWord := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_'
		if isWord {
			b.WriteRune(r)
			lastWasSep = false
		} else if !lastWasSep && b.Len() > 0 {
			b.WriteByte('_')
			lastWasSep = true
		}
	}
	return strings.TrimSuffix(b.String(), "_")
}

// macroNameFromSetCommands derives an ME macro name from one or more
// parsed tag-write commands, per instruction: the name should read as
// "mcr_<tag>_set_<value>" (or "mcr_<tag>_toggle" for a Toggle) so it
// documents what the macro (still to be created by hand) actually needs
// to do.
//
// Multiple commands on one button are chained in order, e.g.
// "mcr_A_set_1_B_toggle" for "&Set A 1;Toggle B".
//
// Returns ok=false (letting the caller fall back to caption-based
// naming) if no command yields a usable tag name.
func macroNameFromSetCommands(setCommands []tagWriteCommand) (name string, ok bool) {
	var parts []string
	for _, cmd := range setCommands {
		tagPart := sanitizeMacroNamePart(cmd.Tag)
		if tagPart == "" {
			continue
		}

		if cmd.IsToggle {
			parts = append(parts, tagPart+"_toggle")
			continue
		}

		valuePart := sanitizeMacroNamePart(cmd.Value)
		if valuePart != "" {
			parts = append(parts, tagPart+"_set_"+valuePart)
		} else {
			parts = append(parts, tagPart+"_set")
		}
	}

	if len(parts) == 0 {
		return "", false
	}
	return "mcr_" + strings.Join(parts, "_"), true
}

// setCommandsDescription renders parsed tag-write commands back into
// human-readable text for warning messages, e.g. "KNET\PROC\PAUSE_CAUSEID
// = 201" or "KNET\PROC\PAUSE_OFF (toggle)" - documenting exactly what the
// still-to-be-created ME macro needs to do for each command, distinct
// from what it needs to do for a plain value write.
func setCommandsDescription(setCommands []tagWriteCommand) string {
	parts := make([]string, 0, len(setCommands))
	for _, cmd := range setCommands {
		if cmd.IsToggle {
			parts = append(parts, fmt.Sprintf("%s (toggle)", cmd.Tag))
		} else {
			parts = append(parts, fmt.Sprintf("%s = %s", cmd.Tag, cmd.Value))
		}
	}
	return strings.Join(parts, "; ")
}

// rebuildAsMacroButton rebuilds an SE <button> whose only commands are
// one or more "&Set <tag> <value>" writes into ME's <macroButton>,
// following the same shape/caption-carrying approach as
// rebuildAsGotoButton/rebuildAsCloseButton but with macroButton's own
// attribute set (macro/UseVariableMacro in place of navigation or close
// attributes) - confirmed against a real ME sample (main.xml). The macro
// name is derived from the tag(s)/value(s) being written (see
// macroNameFromSetCommands), falling back to the button's own caption
// text if that yields nothing usable. ME has no inline "write literal
// value to tag" object, so the macro itself (which must perform the
// equivalent SetTag/write for each "<tag> <value>" pair recorded here)
// still needs to be created by hand in the ME project; a warning listing
// the original SE tag-write commands is always emitted so nothing is
// silently lost.
func rebuildAsMacroButton(node *XNode, name string, upState *XNode, setCommands []tagWriteCommand, parent *XNode, c *Converter) {
	backColor := "navy"
	foreColor := "white"
	if upState != nil {
		backColor = upState.GetAttr("backColor", "navy")
		foreColor = upState.GetAttr("foreColor", "white")
	}

	var upCaption *XNode
	if upState != nil {
		for _, ch := range upState.Children {
			if ch.Tag == "caption" {
				upCaption = ch
			}
		}
	}

	captionText := ""
	if upCaption != nil {
		captionText = upCaption.GetAttr("caption", "")
	}

	// Prefer a macro name derived from what the macro actually needs to
	// do (the tag(s)/value(s) being set) - e.g. "mcr_Pump1_set_1" - since
	// that documents the still-to-be-created macro's purpose better than
	// the button's caption. Fall back to the caption-derived name (and
	// then the button's own name) if the set-command text doesn't yield
	// anything usable.
	macroName, ok := macroNameFromSetCommands(setCommands)
	if !ok {
		macroName = macroNameFromCaption(captionText, name)
	}

	style := node.GetAttr("style", "beveled")
	bevelWidth := node.GetAttr("bevelWidth", "4")
	borderStyle := "none"
	if style == "beveled" {
		borderStyle = "raised"
	}

	height := node.GetAttr("height")
	width := node.GetAttr("width")
	left := node.GetAttr("left")
	top := node.GetAttr("top")
	visible := node.GetAttr("visible", "true")
	wallpaper := node.GetAttr("wallpaper", "false")
	isRef := node.GetAttr("isReferenceObject", "false")

	node.Tag = "macroButton"
	node.ClearAttrs()
	node.ClearChildren()

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
		node.SetAttr("wallpaper", wallpaper)
	}
	node.SetAttr("isReferenceObject", isRef)
	node.SetAttr("macro", macroName)
	node.SetAttr("audio", "true")
	node.SetAttr("backColor", backColor)
	node.SetAttr("backStyle", "solid")
	node.SetAttr("borderStyle", borderStyle)
	node.SetAttr("borderUsesBackColor", "true")
	node.SetAttr("borderWidth", bevelWidth)
	node.SetAttr("description", "")
	node.SetAttr("highlightColor", "lime")
	node.SetAttr("borderColor", backColor)
	node.SetAttr("patternColor", foreColor)
	node.SetAttr("patternStyle", "none")
	node.SetAttr("horizontalMargin", "0")
	node.SetAttr("verticalMargin", "0")
	node.SetAttr("UseVariableMacro", "false")
	node.SetAttr("shape", "rectangle")
	node.SetAttr("touch", "true")
	node.SetAttr("blink", "false")
	node.SetAttr("endColor", "white")
	node.SetAttr("gradientStop", "50")
	node.SetAttr("gradientDirection", "gradientDirectionHorizontal")
	node.SetAttr("gradientShadingStyle", "gradientHorizontalFromRight")

	caption := newXNode("caption")
	if upCaption != nil {
		caption.SetAttr("fontFamily", upCaption.GetAttr("fontFamily", "Arial"))
		caption.SetAttr("fontSize", upCaption.GetAttr("fontSize", "10"))
		caption.SetAttr("bold", upCaption.GetAttr("bold", "false"))
		caption.SetAttr("italic", upCaption.GetAttr("italic", "false"))
		caption.SetAttr("underline", upCaption.GetAttr("underline", "false"))
		caption.SetAttr("strikethrough", upCaption.GetAttr("strikethrough", "false"))
		caption.SetAttr("caption", captionText)
	} else {
		caption.SetAttr("fontFamily", "Arial")
		caption.SetAttr("fontSize", "10")
		caption.SetAttr("bold", "false")
		caption.SetAttr("italic", "false")
		caption.SetAttr("underline", "false")
		caption.SetAttr("strikethrough", "false")
		caption.SetAttr("caption", "")
	}
	caption.SetAttr("color", foreColor)
	caption.SetAttr("backColor", backColor)
	caption.SetAttr("backStyle", "transparent")
	caption.SetAttr("alignment", "middleCenter")
	caption.SetAttr("wordWrap", "true")
	caption.SetAttr("blink", "false")
	node.Children = append(node.Children, caption)

	imageSettings := newXNode("imageSettings")
	imageSettings.SetAttr("imageName", "")
	imageSettings.SetAttr("alignment", "middleCenter")
	imageSettings.SetAttr("backStyle", "transparent")
	imageSettings.SetAttr("color", foreColor)
	imageSettings.SetAttr("backColor", backColor)
	imageSettings.SetAttr("scaled", "false")
	imageSettings.SetAttr("blink", "false")
	node.Children = append(node.Children, imageSettings)

	c.Warnings = append(c.Warnings, fmt.Sprintf(
		"button \"%s\" -> <macroButton macro=\"%s\">. Create ME macro \"%s\" that does: %s.",
		name, macroName, macroName, setCommandsDescription(setCommands)))
}

func rebuildAsGotoButton(node *XNode, name, targetDisplay string, upState *XNode, parent *XNode) {
	backColor := "navy"
	foreColor := "white"
	if upState != nil {
		backColor = upState.GetAttr("backColor", "navy")
		foreColor = upState.GetAttr("foreColor", "white")
	}

	var upCaption *XNode
	if upState != nil {
		for _, c := range upState.Children {
			if c.Tag == "caption" {
				upCaption = c
			}
		}
	}

	style := node.GetAttr("style", "beveled")
	bevelWidth := node.GetAttr("bevelWidth", "4")
	borderStyle := "none"
	if style == "beveled" {
		borderStyle = "raised"
	}

	height := node.GetAttr("height")
	width := node.GetAttr("width")
	left := node.GetAttr("left")
	top := node.GetAttr("top")
	visible := node.GetAttr("visible", "true")
	wallpaper := node.GetAttr("wallpaper", "false")
	isRef := node.GetAttr("isReferenceObject", "false")

	node.Tag = "gotoButton"
	node.ClearAttrs()
	node.ClearChildren()

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
		node.SetAttr("wallpaper", wallpaper)
	}
	node.SetAttr("isReferenceObject", isRef)
	node.SetAttr("audio", "true")
	node.SetAttr("backColor", backColor)
	node.SetAttr("backStyle", "solid")
	node.SetAttr("borderStyle", borderStyle)
	node.SetAttr("borderUsesBackColor", "true")
	node.SetAttr("borderWidth", bevelWidth)
	node.SetAttr("description", "")
	node.SetAttr("highlightColor", "lime")
	node.SetAttr("borderColor", backColor)
	node.SetAttr("patternColor", foreColor)
	node.SetAttr("patternStyle", "none")
	node.SetAttr("horizontalMargin", "0")
	node.SetAttr("verticalMargin", "0")
	node.SetAttr("shape", "rectangle")
	node.SetAttr("touch", "true")
	node.SetAttr("blink", "false")
	node.SetAttr("displayPosition", "false")
	node.SetAttr("displayLeftPosition", "0")
	node.SetAttr("displayTopPosition", "0")
	node.SetAttr("UseVariableDisplay", "false")
	node.SetAttr("UseVariableDisplayPosition", "false")
	node.SetAttr("display", targetDisplay)
	node.SetAttr("parameterFile", "")
	node.SetAttr("parameterList", "")
	node.SetAttr("parameterType", "parameterFile")
	node.SetAttr("captionOnBorder", "false")
	node.SetAttr("endColor", "white")
	node.SetAttr("gradientStop", "50")
	node.SetAttr("gradientDirection", "gradientDirectionHorizontal")
	node.SetAttr("gradientShadingStyle", "gradientHorizontalFromRight")

	caption := newXNode("caption")
	if upCaption != nil {
		caption.SetAttr("fontFamily", upCaption.GetAttr("fontFamily", "Arial"))
		caption.SetAttr("fontSize", upCaption.GetAttr("fontSize", "10"))
		caption.SetAttr("bold", upCaption.GetAttr("bold", "false"))
		caption.SetAttr("italic", upCaption.GetAttr("italic", "false"))
		caption.SetAttr("underline", upCaption.GetAttr("underline", "false"))
		caption.SetAttr("strikethrough", upCaption.GetAttr("strikethrough", "false"))
		caption.SetAttr("caption", upCaption.GetAttr("caption", ""))
	} else {
		caption.SetAttr("fontFamily", "Arial")
		caption.SetAttr("fontSize", "10")
		caption.SetAttr("bold", "false")
		caption.SetAttr("italic", "false")
		caption.SetAttr("underline", "false")
		caption.SetAttr("strikethrough", "false")
		caption.SetAttr("caption", "")
	}
	caption.SetAttr("color", foreColor)
	caption.SetAttr("backColor", backColor)
	caption.SetAttr("backStyle", "transparent")
	caption.SetAttr("alignment", "middleCenter")
	caption.SetAttr("wordWrap", "true")
	caption.SetAttr("blink", "false")
	node.Children = append(node.Children, caption)

	imageSettings := newXNode("imageSettings")
	imageSettings.SetAttr("imageName", "")
	imageSettings.SetAttr("alignment", "middleCenter")
	imageSettings.SetAttr("backStyle", "transparent")
	imageSettings.SetAttr("color", foreColor)
	imageSettings.SetAttr("backColor", backColor)
	imageSettings.SetAttr("scaled", "false")
	imageSettings.SetAttr("blink", "false")
	node.Children = append(node.Children, imageSettings)
}

// rebuildAsCloseButton rebuilds an SE <button> whose only command is
// "Abort <name>" (no accompanying Display) into ME's <closeButton>,
// following the same shape/caption-carrying approach as
// rebuildAsGotoButton but with closeButton's own attribute set
// (closeValue/writeOnClose in place of the gotoButton's display-target
// attributes) - confirmed against a real ME sample (ME_BTNS.xml).
func rebuildAsCloseButton(node *XNode, name string, upState *XNode, parent *XNode) {
	backColor := "navy"
	foreColor := "white"
	if upState != nil {
		backColor = upState.GetAttr("backColor", "navy")
		foreColor = upState.GetAttr("foreColor", "white")
	}

	var upCaption *XNode
	if upState != nil {
		for _, c := range upState.Children {
			if c.Tag == "caption" {
				upCaption = c
			}
		}
	}

	style := node.GetAttr("style", "beveled")
	bevelWidth := node.GetAttr("bevelWidth", "4")
	borderStyle := "none"
	if style == "beveled" {
		borderStyle = "raised"
	}

	height := node.GetAttr("height")
	width := node.GetAttr("width")
	left := node.GetAttr("left")
	top := node.GetAttr("top")
	visible := node.GetAttr("visible", "true")
	wallpaper := node.GetAttr("wallpaper", "false")
	isRef := node.GetAttr("isReferenceObject", "false")

	node.Tag = "closeButton"
	node.ClearAttrs()
	node.ClearChildren()

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
		node.SetAttr("wallpaper", wallpaper)
	}
	node.SetAttr("isReferenceObject", isRef)
	node.SetAttr("audio", "true")
	node.SetAttr("backColor", backColor)
	node.SetAttr("backStyle", "solid")
	node.SetAttr("borderStyle", borderStyle)
	node.SetAttr("borderUsesBackColor", "true")
	node.SetAttr("borderWidth", bevelWidth)
	node.SetAttr("description", "")
	node.SetAttr("highlightColor", "lime")
	node.SetAttr("borderColor", backColor)
	node.SetAttr("patternColor", foreColor)
	node.SetAttr("patternStyle", "none")
	node.SetAttr("horizontalMargin", "0")
	node.SetAttr("verticalMargin", "0")
	node.SetAttr("shape", "rectangle")
	node.SetAttr("touch", "true")
	node.SetAttr("blink", "false")
	node.SetAttr("closeValue", "0")
	node.SetAttr("writeOnClose", "false")
	node.SetAttr("endColor", "white")
	node.SetAttr("gradientStop", "50")
	node.SetAttr("gradientDirection", "gradientDirectionHorizontal")
	node.SetAttr("gradientShadingStyle", "gradientHorizontalFromRight")

	caption := newXNode("caption")
	if upCaption != nil {
		caption.SetAttr("fontFamily", upCaption.GetAttr("fontFamily", "Arial"))
		caption.SetAttr("fontSize", upCaption.GetAttr("fontSize", "10"))
		caption.SetAttr("bold", upCaption.GetAttr("bold", "false"))
		caption.SetAttr("italic", upCaption.GetAttr("italic", "false"))
		caption.SetAttr("underline", upCaption.GetAttr("underline", "false"))
		caption.SetAttr("strikethrough", upCaption.GetAttr("strikethrough", "false"))
		caption.SetAttr("caption", upCaption.GetAttr("caption", ""))
	} else {
		caption.SetAttr("fontFamily", "Arial")
		caption.SetAttr("fontSize", "10")
		caption.SetAttr("bold", "false")
		caption.SetAttr("italic", "false")
		caption.SetAttr("underline", "false")
		caption.SetAttr("strikethrough", "false")
		caption.SetAttr("caption", "")
	}
	caption.SetAttr("color", foreColor)
	caption.SetAttr("backColor", backColor)
	caption.SetAttr("backStyle", "transparent")
	caption.SetAttr("alignment", "middleCenter")
	caption.SetAttr("wordWrap", "true")
	caption.SetAttr("blink", "false")
	node.Children = append(node.Children, caption)

	imageSettings := newXNode("imageSettings")
	imageSettings.SetAttr("imageName", "")
	imageSettings.SetAttr("alignment", "middleCenter")
	imageSettings.SetAttr("backStyle", "transparent")
	imageSettings.SetAttr("color", foreColor)
	imageSettings.SetAttr("backColor", backColor)
	imageSettings.SetAttr("scaled", "false")
	imageSettings.SetAttr("blink", "false")
	node.Children = append(node.Children, imageSettings)
}

// rebuildAsLoginButton rebuilds an SE <button> whose only command
// launches the RsvLogin helper (see parseLoginLogoutCommand) into ME's
// native <loginButton>, following the same shape/caption-carrying
// approach as rebuildAsGotoButton/rebuildAsCloseButton. Attribute names
// and defaults (domainNameVisible, domainNameDisable, hideUserNameEntry,
// useVariableDomainName, domainName) are taken from a real ME sample
// (MAIN.xml); SE's RsvLogin command-line flags (/OnTop, /Qwerty,
// /HiedeHelpAbout) have no per-flag ME equivalent on <loginButton> - ME's
// login prompt behavior isn't independently configurable the same way -
// so they're intentionally not carried over, only implied by using the
// native element at all.
func rebuildAsLoginButton(node *XNode, name string, upState *XNode, parent *XNode) {
	backColor := "navy"
	foreColor := "white"
	if upState != nil {
		backColor = upState.GetAttr("backColor", "navy")
		foreColor = upState.GetAttr("foreColor", "white")
	}

	var upCaption *XNode
	if upState != nil {
		for _, c := range upState.Children {
			if c.Tag == "caption" {
				upCaption = c
			}
		}
	}

	style := node.GetAttr("style", "beveled")
	bevelWidth := node.GetAttr("bevelWidth", "4")
	borderStyle := "none"
	if style == "beveled" {
		borderStyle = "raised"
	}

	height := node.GetAttr("height")
	width := node.GetAttr("width")
	left := node.GetAttr("left")
	top := node.GetAttr("top")
	visible := node.GetAttr("visible", "true")
	wallpaper := node.GetAttr("wallpaper", "false")
	isRef := node.GetAttr("isReferenceObject", "false")

	node.Tag = "loginButton"
	node.ClearAttrs()
	node.ClearChildren()

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
		node.SetAttr("wallpaper", wallpaper)
	}
	node.SetAttr("isReferenceObject", isRef)
	node.SetAttr("audio", "true")
	node.SetAttr("backColor", backColor)
	node.SetAttr("backStyle", "solid")
	node.SetAttr("borderStyle", borderStyle)
	node.SetAttr("borderUsesBackColor", "true")
	node.SetAttr("borderWidth", bevelWidth)
	node.SetAttr("description", "")
	node.SetAttr("highlightColor", "lime")
	node.SetAttr("borderColor", backColor)
	node.SetAttr("patternColor", foreColor)
	node.SetAttr("patternStyle", "none")
	node.SetAttr("horizontalMargin", "0")
	node.SetAttr("verticalMargin", "0")
	node.SetAttr("shape", "rectangle")
	node.SetAttr("touch", "true")
	node.SetAttr("blink", "false")
	node.SetAttr("endColor", "white")
	node.SetAttr("gradientStop", "50")
	node.SetAttr("gradientDirection", "gradientDirectionHorizontal")
	node.SetAttr("gradientShadingStyle", "gradientHorizontalFromRight")
	node.SetAttr("domainNameVisible", "false")
	node.SetAttr("domainNameDisable", "false")
	node.SetAttr("hideUserNameEntry", "false")
	node.SetAttr("useVariableDomainName", "false")
	node.SetAttr("domainName", "")

	caption := newXNode("caption")
	if upCaption != nil {
		caption.SetAttr("fontFamily", upCaption.GetAttr("fontFamily", "Arial"))
		caption.SetAttr("fontSize", upCaption.GetAttr("fontSize", "10"))
		caption.SetAttr("bold", upCaption.GetAttr("bold", "false"))
		caption.SetAttr("italic", upCaption.GetAttr("italic", "false"))
		caption.SetAttr("underline", upCaption.GetAttr("underline", "false"))
		caption.SetAttr("strikethrough", upCaption.GetAttr("strikethrough", "false"))
		caption.SetAttr("caption", upCaption.GetAttr("caption", ""))
	} else {
		caption.SetAttr("fontFamily", "Arial")
		caption.SetAttr("fontSize", "10")
		caption.SetAttr("bold", "false")
		caption.SetAttr("italic", "false")
		caption.SetAttr("underline", "false")
		caption.SetAttr("strikethrough", "false")
		caption.SetAttr("caption", "")
	}
	caption.SetAttr("color", foreColor)
	caption.SetAttr("backColor", backColor)
	caption.SetAttr("backStyle", "transparent")
	caption.SetAttr("alignment", "middleCenter")
	caption.SetAttr("wordWrap", "true")
	caption.SetAttr("blink", "false")
	node.Children = append(node.Children, caption)

	imageSettings := newXNode("imageSettings")
	imageSettings.SetAttr("imageName", "")
	imageSettings.SetAttr("alignment", "middleCenter")
	imageSettings.SetAttr("backStyle", "transparent")
	imageSettings.SetAttr("color", foreColor)
	imageSettings.SetAttr("backColor", backColor)
	imageSettings.SetAttr("scaled", "false")
	imageSettings.SetAttr("blink", "false")
	node.Children = append(node.Children, imageSettings)
}

// rebuildAsLogoutButton rebuilds an SE <button> whose only command is
// the bare "Logoff" (see parseLoginLogoutCommand) into ME's native
// <logoutButton>, following the same shape/caption-carrying approach as
// rebuildAsGotoButton/rebuildAsCloseButton. Attribute names and defaults
// (showDisplayOnLogout, display, parameterFile, parameterList,
// parameterType) are taken from a real ME sample (MAIN.xml); SE's bare
// "Logoff" carries no navigate-on-logout target, so showDisplayOnLogout
// defaults to false/empty, matching the sample.
func rebuildAsLogoutButton(node *XNode, name string, upState *XNode, parent *XNode) {
	backColor := "navy"
	foreColor := "white"
	if upState != nil {
		backColor = upState.GetAttr("backColor", "navy")
		foreColor = upState.GetAttr("foreColor", "white")
	}

	var upCaption *XNode
	if upState != nil {
		for _, c := range upState.Children {
			if c.Tag == "caption" {
				upCaption = c
			}
		}
	}

	style := node.GetAttr("style", "beveled")
	bevelWidth := node.GetAttr("bevelWidth", "4")
	borderStyle := "none"
	if style == "beveled" {
		borderStyle = "raised"
	}

	height := node.GetAttr("height")
	width := node.GetAttr("width")
	left := node.GetAttr("left")
	top := node.GetAttr("top")
	visible := node.GetAttr("visible", "true")
	wallpaper := node.GetAttr("wallpaper", "false")
	isRef := node.GetAttr("isReferenceObject", "false")

	node.Tag = "logoutButton"
	node.ClearAttrs()
	node.ClearChildren()

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
		node.SetAttr("wallpaper", wallpaper)
	}
	node.SetAttr("isReferenceObject", isRef)
	node.SetAttr("audio", "true")
	node.SetAttr("backColor", backColor)
	node.SetAttr("backStyle", "solid")
	node.SetAttr("borderStyle", borderStyle)
	node.SetAttr("borderUsesBackColor", "true")
	node.SetAttr("borderWidth", bevelWidth)
	node.SetAttr("description", "")
	node.SetAttr("highlightColor", "lime")
	node.SetAttr("borderColor", backColor)
	node.SetAttr("patternColor", foreColor)
	node.SetAttr("patternStyle", "none")
	node.SetAttr("horizontalMargin", "0")
	node.SetAttr("verticalMargin", "0")
	node.SetAttr("shape", "rectangle")
	node.SetAttr("touch", "true")
	node.SetAttr("blink", "false")
	node.SetAttr("endColor", "white")
	node.SetAttr("gradientStop", "50")
	node.SetAttr("gradientDirection", "gradientDirectionHorizontal")
	node.SetAttr("gradientShadingStyle", "gradientHorizontalFromRight")
	node.SetAttr("showDisplayOnLogout", "false")
	node.SetAttr("display", "")
	node.SetAttr("parameterFile", "")
	node.SetAttr("parameterList", "")
	node.SetAttr("parameterType", "parameterFile")

	caption := newXNode("caption")
	if upCaption != nil {
		caption.SetAttr("fontFamily", upCaption.GetAttr("fontFamily", "Arial"))
		caption.SetAttr("fontSize", upCaption.GetAttr("fontSize", "10"))
		caption.SetAttr("bold", upCaption.GetAttr("bold", "false"))
		caption.SetAttr("italic", upCaption.GetAttr("italic", "false"))
		caption.SetAttr("underline", upCaption.GetAttr("underline", "false"))
		caption.SetAttr("strikethrough", upCaption.GetAttr("strikethrough", "false"))
		caption.SetAttr("caption", upCaption.GetAttr("caption", ""))
	} else {
		caption.SetAttr("fontFamily", "Arial")
		caption.SetAttr("fontSize", "10")
		caption.SetAttr("bold", "false")
		caption.SetAttr("italic", "false")
		caption.SetAttr("underline", "false")
		caption.SetAttr("strikethrough", "false")
		caption.SetAttr("caption", "")
	}
	caption.SetAttr("color", foreColor)
	caption.SetAttr("backColor", backColor)
	caption.SetAttr("backStyle", "transparent")
	caption.SetAttr("alignment", "middleCenter")
	caption.SetAttr("wordWrap", "true")
	caption.SetAttr("blink", "false")
	node.Children = append(node.Children, caption)

	imageSettings := newXNode("imageSettings")
	imageSettings.SetAttr("imageName", "")
	imageSettings.SetAttr("alignment", "middleCenter")
	imageSettings.SetAttr("backStyle", "transparent")
	imageSettings.SetAttr("color", foreColor)
	imageSettings.SetAttr("backColor", backColor)
	imageSettings.SetAttr("scaled", "false")
	imageSettings.SetAttr("blink", "false")
	node.Children = append(node.Children, imageSettings)
}

func (c *Converter) displaySettingsSeToMe(node *XNode) {
	displayType := node.GetAttr("displayType", "replace")
	if displayType == "overlay" {
		displayType = "onTop"
		node.SetAttr("displayType", "onTop")
		c.Warnings = append(c.Warnings, "displayType \"overlay\" remapped to ME \"onTop\" - verify visually.")
	}

	position := node.GetAttr("position", "useCurrentPosition")
	node.RemoveAttr("position")

	if displayType == "replace" {
		node.RemoveAttr("positionX")
		node.RemoveAttr("positionY")
		node.RemoveAttr("titleBar")
		node.RemoveAttr("size")
		node.RemoveAttr("cannotBeReplaced")
	} else {
		if position == "specifyPositionInPixels" {
			if !node.Has("positionX") {
				node.SetAttr("positionX", "0")
			}
			if !node.Has("positionY") {
				node.SetAttr("positionY", "0")
			}
		} else {
			node.SetAttr("positionX", node.GetAttr("positionX", "0"))
			node.SetAttr("positionY", node.GetAttr("positionY", "0"))
		}
		if !node.Has("cannotBeReplaced") {
			node.SetAttr("cannotBeReplaced", "false")
		}
	}

	if !node.Has("displayNumber") {
		node.SetAttr("displayNumber", "0")
	}

	node.RenameAttr("startupCommand", "startupMacro")
	node.RenameAttr("shutdownCommand", "shutdownMacro")

	if !node.Has("disableInitialInputFocus") {
		node.SetAttr("disableInitialInputFocus", "false")
	}

	for _, a := range seDisplaySettingsOnlyAttrs {
		node.RemoveAttr(a)
	}

	stripSeOnlyCommonAttrs(node)
}

func (c *Converter) numericDisplaySeToMe(node *XNode) {
	justification := node.GetAttr("justification", "right")
	node.RemoveAttr("justification")
	node.SetAttr("alignment", justificationToAlignment(justification))

	if node.Has("fieldLength") {
		node.SetAttr("numberOfDigits", node.GetAttr("fieldLength"))
		node.RemoveAttr("fieldLength")
	} else if !node.Has("numberOfDigits") {
		node.SetAttr("numberOfDigits", "5")
	}

	node.RenameAttr("leadingCharacter", "fillLeftWith")
	if !node.Has("fillLeftWith") {
		node.SetAttr("fillLeftWith", "spaces")
	} else if node.GetAttr("fillLeftWith") == "blanks" {
		node.SetAttr("fillLeftWith", "spaces")
	}

	for _, a := range numericMeOnlyDropAttrs {
		node.RemoveAttr(a)
	}

	if node.Has("charWidth") || node.Has("charHeight") {
		if !node.Has("fontSize") {
			ch := parseInvariantDouble(node.GetAttr("charHeight", "14"), 14.0)
			node.SetAttr("fontSize", formatInvariantDouble(math.Max(6.0, ch*0.72)))
		}
		node.RemoveAttr("charWidth")
		node.RemoveAttr("charHeight")
		c.Warnings = append(c.Warnings, fmt.Sprintf("<numericDisplay name=\"%s\">: fontSize computed from SE charWidth/charHeight - check actual size.", node.GetAttr("name")))
	}

	if !node.Has("borderColor") {
		node.SetAttr("borderColor", node.GetAttr("foreColor", "black"))
	}
	ensureDefaults(node, fieldDisplaySeToMeDefaults)

	stripSeOnlyCommonAttrs(node)
}

func (c *Converter) stringDisplaySeToMe(node *XNode) {
	justification := node.GetAttr("justification", "left")
	node.RemoveAttr("justification")
	node.SetAttr("alignment", justificationToAlignment(justification))

	for _, a := range stringDisplaySeOnlySizingAttrs {
		node.RemoveAttr(a)
	}

	if !node.Has("wordWrap") {
		node.SetAttr("wordWrap", "true")
	}

	if !node.Has("borderColor") {
		node.SetAttr("borderColor", node.GetAttr("foreColor", "black"))
	}
	ensureDefaults(node, fieldDisplaySeToMeDefaults)

	stripSeOnlyCommonAttrs(node)
}

// numericInputSeToMe rebuilds SE <numericInput> (a plain rectangular entry
// field, tab-order based, no ME-style dedicated keypad popup config) as ME
// <numericInputCursorPoint> (a cursor-point field driven by a physical
// keypad on the terminal). The two are not attribute-compatible, so this
// is a full attribute rewrite rather than a rename/strip pass. Anything
// under <connections> (Value expression) is preserved as-is by the
// caller's normal child recursion; SE's <confirm>/<eSignature> children
// are dropped by the caller (no ME equivalent).
func (c *Converter) numericInputSeToMe(node *XNode, parent *XNode) {
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
	fieldLength := node.GetAttr("fieldLength", "6")
	decimalPlaces := node.GetAttr("decimalPlaces", "0")
	justification := node.GetAttr("justification", "left")

	node.ClearAttrs()
	node.Tag = "numericInputCursorPoint"

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
	node.SetAttr("isReferenceObject", "false")
	node.SetAttr("alignment", justificationToAlignment(justification))
	node.SetAttr("audio", "true")
	node.SetAttr("backStyle", "solid")
	node.SetAttr("backColor", "#0000C0")
	node.SetAttr("foreColor", "white")
	node.SetAttr("blink", "false")
	node.SetAttr("borderStyle", "raisedInset")
	node.SetAttr("borderUsesBackColor", "true")
	node.SetAttr("borderWidth", "8")
	node.SetAttr("description", "")
	node.SetAttr("highlightColor", "lime")
	node.SetAttr("borderColor", "#0000C0")
	node.SetAttr("patternColor", "white")
	node.SetAttr("patternStyle", "none")
	node.SetAttr("touch", "false")
	node.SetAttr("horizontalMargin", "0")
	node.SetAttr("verticalMargin", "0")
	node.SetAttr("enterKeyControlDelay", "400")
	node.SetAttr("enterKeyHandshakeTime", "4")
	node.SetAttr("enterKeyHoldTime", "250")
	node.SetAttr("handshakeReset", "nonZeroValue")
	node.SetAttr("keyNavigation", "false")
	node.SetAttr("decimalPoint", "fixedPosition")
	node.SetAttr("digitsAfterDecimalPoint", decimalPlaces)
	node.SetAttr("numberOfDigits", fieldLength)
	node.SetAttr("decimalPlaces", decimalPlaces)
	node.SetAttr("fixedPosition", "displayed")
	node.SetAttr("fillLeftWith", "spaces")
	node.SetAttr("numericPopup", "scratchpad")
	node.SetAttr("fontFamily", fontFamily)
	node.SetAttr("fontSize", fontSize)
	node.SetAttr("bold", bold)
	node.SetAttr("italic", italic)
	node.SetAttr("underline", underline)
	node.SetAttr("strikethrough", strikethrough)
	node.SetAttr("rampValue", "1")
	node.SetAttr("useVariableMinMax", "false")
	node.SetAttr("captionOnPad", "")
	node.SetAttr("minValue", "0")
	node.SetAttr("maxValue", "9999")
	node.SetAttr("endColor", "white")
	node.SetAttr("gradientStop", "50")
	node.SetAttr("gradientDirection", "gradientDirectionHorizontal")
	node.SetAttr("gradientShadingStyle", "gradientHorizontalFromRight")
	node.SetAttr("RequireElectronicSignature", "false")
	node.SetAttr("AllowBlankComment", "false")
	node.SetAttr("RequireReAuthentication", "false")
	node.SetAttr("RequireCounterSignature", "false")
	node.SetAttr("AuthorizedGroup", "")
	node.SetAttr("ESDomainNameVisible", "false")
	node.SetAttr("ESDomainNameType", "ESDomainNameConstant")
	node.SetAttr("ESDomainName", "")
	node.SetAttr("VariableDomainName", "")
	node.SetAttr("ESDomainNameDisable", "false")

	c.Warnings = append(c.Warnings, fmt.Sprintf("<numericInput name=\"%s\"> -> <numericInputCursorPoint>: min/max set to 0/9999, adjust manually.", name))
}

// stringInputSeToMe rebuilds SE <stringInput> (a plain rectangular
// text-entry field) into ME's closest string-entry object,
// <stringInputEnable> - a caption+keypad-popup button rather than a plain
// field, since ME has no cursor-point equivalent for string entry. The
// attribute schema was confirmed against a real ME sample (ME_All.xml):
// notably numberOfInputCharacters (not fieldLength), fillCharacter,
// maskScratchpad, takeFocusOnPress, and stringPopup="keyboard". The
// caption is left blank (matching the sample default) since SE has no
// caption text to carry over. The operator experience still changes
// (tap-to-open-keyboard instead of an inline field), so this always
// warns.
func (c *Converter) stringInputSeToMe(node *XNode, parent *XNode) {
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
	foreColor := node.GetAttr("foreColor", "white")
	backColor := node.GetAttr("backColor", "#0000C0")
	dimensionsWidth := node.GetAttr("dimensionsWidth", "16")

	node.ClearAttrs()
	node.ClearChildren()
	node.Tag = "stringInputEnable"

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
	node.SetAttr("isReferenceObject", "false")
	node.SetAttr("audio", "true")
	node.SetAttr("backColor", backColor)
	node.SetAttr("backStyle", "solid")
	node.SetAttr("borderStyle", "raised")
	node.SetAttr("borderUsesBackColor", "true")
	node.SetAttr("borderWidth", "4")
	node.SetAttr("description", "")
	node.SetAttr("highlightColor", "lime")
	node.SetAttr("borderColor", backColor)
	node.SetAttr("patternColor", foreColor)
	node.SetAttr("patternStyle", "none")
	node.SetAttr("horizontalMargin", "0")
	node.SetAttr("verticalMargin", "0")
	node.SetAttr("shape", "rectangle")
	node.SetAttr("touch", "true")
	node.SetAttr("blink", "false")
	node.SetAttr("keyNavigation", "false")
	node.SetAttr("maskScratchpad", "false")
	node.SetAttr("takeFocusOnPress", "false")
	node.SetAttr("numberOfInputCharacters", dimensionsWidth)
	node.SetAttr("fillCharacter", "null")
	node.SetAttr("stringPopup", "keyboard")
	node.SetAttr("captionOnBorder", "false")
	node.SetAttr("enterKeyControlDelay", "400")
	node.SetAttr("enterKeyHandshakeTime", "4")
	node.SetAttr("enterKeyHoldTime", "250")
	node.SetAttr("handshakeReset", "nonZeroValue")
	node.SetAttr("endColor", "white")
	node.SetAttr("gradientStop", "50")
	node.SetAttr("gradientDirection", "gradientDirectionHorizontal")
	node.SetAttr("gradientShadingStyle", "gradientHorizontalFromRight")
	node.SetAttr("RequireElectronicSignature", "false")
	node.SetAttr("AllowBlankComment", "false")
	node.SetAttr("RequireReAuthentication", "false")
	node.SetAttr("RequireCounterSignature", "false")
	node.SetAttr("AuthorizedGroup", "")
	node.SetAttr("ESDomainNameVisible", "false")
	node.SetAttr("ESDomainNameType", "ESDomainNameConstant")
	node.SetAttr("ESDomainName", "")
	node.SetAttr("VariableDomainName", "")
	node.SetAttr("ESDomainNameDisable", "false")

	caption := newXNode("caption")
	caption.SetAttr("fontFamily", fontFamily)
	caption.SetAttr("fontSize", fontSize)
	caption.SetAttr("bold", bold)
	caption.SetAttr("italic", italic)
	caption.SetAttr("underline", underline)
	caption.SetAttr("strikethrough", strikethrough)
	caption.SetAttr("caption", "")
	caption.SetAttr("color", foreColor)
	caption.SetAttr("backColor", backColor)
	caption.SetAttr("backStyle", "transparent")
	caption.SetAttr("alignment", "middleCenter")
	caption.SetAttr("wordWrap", "true")
	caption.SetAttr("blink", "false")
	node.Children = append(node.Children, caption)

	imageSettings := newXNode("imageSettings")
	imageSettings.SetAttr("imageName", "")
	imageSettings.SetAttr("alignment", "middleCenter")
	imageSettings.SetAttr("backStyle", "transparent")
	imageSettings.SetAttr("color", foreColor)
	imageSettings.SetAttr("backColor", backColor)
	imageSettings.SetAttr("scaled", "false")
	imageSettings.SetAttr("blink", "false")
	node.Children = append(node.Children, imageSettings)

	c.Warnings = append(c.Warnings, fmt.Sprintf("<stringInput name=\"%s\"> -> <stringInputEnable>: now a keypad-popup button, not inline - verify manually.", name))
}
