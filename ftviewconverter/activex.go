package ftviewconverter

import "fmt"

// multistateIndicatorClassID identifies the MultiState Indicator control
// for RSView32 (Multistate_Indicator.ocx). Unlike every other <activeX>
// control, we've reverse engineered this one's binary OLE property-bag
// format (base64 `data` attribute) well enough to decode it into ME's
// native <multistateIndicator> element instead of just passing the opaque
// blob through. See multistate_indicator_reverse_engineering.txt for the
// full derivation and the list of properties that are still not fully
// nailed down (the checksum/hash algorithm on each property entry was
// never identified, the leading 4-byte top-level marker is unexplained,
// and a few statealignment enum members beyond middleCenter/bottomRight
// are inferred from the presumed 3x3 grid naming convention rather than
// directly observed on the wire).
//
// Because the checksum algorithm is unknown, we can decode this format
// but cannot re-encode it: there is no way to produce new checksum bytes
// that the real Multistate_Indicator.ocx control will accept. This means
// the conversion is one-way (SE -> ME only). Going the other way (ME ->
// SE) a <multistateIndicator> element is dropped rather than converted;
// see multistateIndicatorMeToSe below.
const multistateIndicatorClassID = "{426E8EFA-89A5-4C43-8658-35A607F0075F}"

// activeXCrossEdition handles <activeX>, which hosts a registered COM/OCX
// control (Trend viewer, Alarm banner, web browser, third-party gauge,
// ...) via its `classId` attribute. Most of the wrapper element's layout
// attributes are shared between editions, but SE also carries
// toolTipText/exposeToVba/tabIndex/pointerHighlight, none of which appear
// on ME's <activeX> (confirmed against ME_All.xml/SE_All.xml samples) -
// those are stripped/added via stripSeOnlyCommonAttrs/addSeCommonDefaults
// like on any other object. The classId GUID identifies a specific
// registered control on the *source* platform's machine and is very
// unlikely to be the same GUID (or to be registered at all) on the target
// platform - FactoryTalk's own ME and SE ship different ActiveX controls
// even for equivalent widgets (e.g. the SE "FactoryTalk Alarm and Event
// Banner" vs ME's alarm objects are not the same control). We keep the
// object (rather than dropping it, since a human may still want to see it
// and swap in the right control) but always warn loudly, because a wrong
// or unregistered classId will fail to load at runtime on the target
// platform even though the XML itself is schema-valid.
//
// EXCEPTION: the MultiState Indicator control (see
// multistateIndicatorClassID above) is understood well enough to decode
// for real on SE->ME, so that direction bypasses all of the above and
// rewrites the node into a native <multistateIndicator> element instead.
func (c *Converter) activeXCrossEdition(node *XNode, direction Direction) {
	name := node.GetAttr("name")
	classID := node.GetAttr("classId", "")

	if direction == SeToMe && classID == multistateIndicatorClassID {
		if c.tryConvertMultistateIndicatorSeToMe(node) {
			return
		}
		// Decoding failed (unexpected/corrupt blob shape) - fall through
		// to the generic opaque-passthrough behavior below rather than
		// silently dropping the object.
		c.Warnings = append(c.Warnings, fmt.Sprintf("<activeX name=\"%s\"> (MultiState Indicator): failed to decode, kept opaque.", name))
	}

	c.Warnings = append(c.Warnings, fmt.Sprintf("<activeX name=\"%s\"> classId %s unchanged - verify it's registered on target platform.", name, classID))

	if direction == SeToMe {
		stripSeOnlyCommonAttrs(node)
	} else {
		addSeCommonDefaults(node)
	}
}

// multistateIndicatorMeToSe handles ME -> SE, which has no way back for
// the MultiState Indicator: the OCX's property-bag checksum algorithm was
// never reverse engineered (see the reverse-engineering guide's open
// items), so no valid binary blob can be synthesized from a decoded
// <multistateIndicator> element. Emitting a blob with fabricated/zeroed
// checksums would silently fail to load in RSView32/FactoryTalk SE at
// runtime despite being schema-valid XML - worse than not converting at
// all. We drop the element and warn loudly so a human can rebuild it
// manually in the SE editor instead.
func (c *Converter) multistateIndicatorMeToSe(node *XNode) {
	name := node.GetAttr("name", "")
	c.Warnings = append(c.Warnings, fmt.Sprintf("<multistateIndicator name=\"%s\"> removed - SE binary format can't be regenerated, rebuild manually.", name))
	markForRemoval(node)
}
