package compat

import "sort"

// Declarative rule registries mapping HTML elements, attributes, image formats,
// and CSS constructs to caniemail.com feature slugs. Every slug referenced here
// is verified against the embedded dataset by TestAllRuleSlugsExistInDataset.

// htmlElementRules maps lowercase HTML tag names to caniemail feature slugs.
var htmlElementRules = map[string]string{
	"abbr":       "html-abbr",
	"acronym":    "html-acronym",
	"address":    "html-address",
	"area":       "html-image-maps",
	"article":    "html-semantics",
	"aside":      "html-semantics",
	"audio":      "html-audio",
	"base":       "html-base",
	"bdi":        "html-bdi",
	"blockquote": "html-blockquote",
	"body":       "html-body",
	"code":       "html-code",
	"del":        "html-del",
	"dfn":        "html-dfn",
	"dialog":     "html-dialog",
	"div":        "html-div",
	"dl":         "html-lists",
	"figcaption": "html-semantics",
	"figure":     "html-semantics",
	"footer":     "html-semantics",
	"form":       "html-form",
	"h1":         "html-h1-h6",
	"h2":         "html-h1-h6",
	"h3":         "html-h1-h6",
	"h4":         "html-h1-h6",
	"h5":         "html-h1-h6",
	"h6":         "html-h1-h6",
	"header":     "html-semantics",
	"hr":         "html-hr",
	"img":        "html-img",
	"link":       "html-link",
	"main":       "html-semantics",
	"map":        "html-image-maps",
	"mark":       "html-semantics",
	"marquee":    "html-marquee",
	"meter":      "html-meter",
	"nav":        "html-semantics",
	"object":     "html-object",
	"ol":         "html-lists",
	"p":          "html-p",
	"picture":    "html-picture",
	"pre":        "html-pre",
	"progress":   "html-progress",
	"rp":         "html-rp",
	"rt":         "html-rt",
	"ruby":       "html-ruby",
	"section":    "html-semantics",
	"select":     "html-select",
	"small":      "html-small",
	"span":       "html-span",
	"strike":     "html-strike",
	"strong":     "html-strong",
	"style":      "html-style",
	"svg":        "html-svg",
	"table":      "html-table",
	"textarea":   "html-textarea",
	"time":       "html-semantics",
	"ul":         "html-lists",
	"video":      "html-video",
	"wbr":        "html-wbr",
}

// htmlInputTypeRules maps lowercase <input type="..."> values to slugs.
var htmlInputTypeRules = map[string]string{
	"checkbox": "html-input-checkbox",
	"hidden":   "html-input-hidden",
	"radio":    "html-input-radio",
	"reset":    "html-input-reset",
	"submit":   "html-input-submit",
	"text":     "html-input-text",
}

// htmlButtonTypeRules maps lowercase <button type="..."> values to slugs.
var htmlButtonTypeRules = map[string]string{
	"reset":  "html-button-reset",
	"submit": "html-button-submit",
}

// htmlAttrRules maps lowercase HTML attribute names to caniemail feature slugs.
var htmlAttrRules = map[string]string{
	"align":            "html-align",
	"aria-describedby": "html-aria-describedby",
	"aria-hidden":      "html-aria-hidden",
	"aria-label":       "html-aria-label",
	"aria-labelledby":  "html-aria-labelledby",
	"aria-live":        "html-aria-live",
	"background":       "html-background",
	"cellpadding":      "html-cellpadding",
	"cellspacing":      "html-cellspacing",
	"command":          "html-command-attribute",
	"commandfor":       "html-command-attribute",
	"dir":              "html-dir",
	"height":           "html-height",
	"hidden":           "html-hidden",
	"inert":            "css-inert-attribute",
	"lang":             "html-lang",
	"loading":          "html-loading-attribute",
	"popover":          "html-popover",
	"required":         "html-required",
	"role":             "html-role",
	"sizes":            "html-srcset",
	"srcset":           "html-srcset",
	"target":           "html-target",
	"valign":           "html-valign",
	"width":            "html-width",
}

// htmlSpecialSlugs holds HTML feature slugs detected via dedicated checks
// (doctype, comments, links, meta tags).
var htmlSpecialSlugs = []string{
	"html-doctype",
	"html-comments",
	"html-mailto-links",
	"html-anchor-links",
	"html-meta-color-scheme",
}

// imageExtRules maps lowercase file extensions (without leading dot) to image format slugs.
var imageExtRules = map[string]string{
	"apng": "image-apng",
	"avif": "image-avif",
	"bmp":  "image-bmp",
	"gif":  "image-gif",
	"hdr":  "image-hdr",
	"heic": "image-heif",
	"heif": "image-heif",
	"ico":  "image-ico",
	"jpeg": "image-jpg",
	"jpg":  "image-jpg",
	"mp4":  "image-mp4",
	"png":  "image-png",
	"svg":  "image-svg",
	"tif":  "image-tiff",
	"tiff": "image-tiff",
	"webp": "image-webp",
}

// imageMIMERules maps lowercase MIME types (data: URIs and inline CID parts) to image format slugs.
var imageMIMERules = map[string]string{
	"image/apng":               "image-apng",
	"image/avif":               "image-avif",
	"image/bmp":                "image-bmp",
	"image/gif":                "image-gif",
	"image/heic":               "image-heif",
	"image/heif":               "image-heif",
	"image/jpeg":               "image-jpg",
	"image/jpg":                "image-jpg",
	"image/png":                "image-png",
	"image/svg+xml":            "image-svg",
	"image/tiff":               "image-tiff",
	"image/vnd.microsoft.icon": "image-ico",
	"image/webp":               "image-webp",
	"image/x-icon":             "image-ico",
	"video/mp4":                "image-mp4",
}

// imageSpecialSlugs lists special image slugs beyond extension/MIME tables.
var imageSpecialSlugs = []string{
	"image-base64",
}

// cssAtRuleRules maps lowercase at-rule names (without leading '@') to slugs.
var cssAtRuleRules = map[string]string{
	"font-face":         "css-at-font-face",
	"import":            "css-at-import",
	"keyframes":         "css-at-keyframes",
	"-webkit-keyframes": "css-at-keyframes",
	"media":             "css-at-media",
	"supports":          "css-at-supports",
}

// cssMediaFeatureRules maps lowercase @media feature names to slugs.
var cssMediaFeatureRules = map[string]string{
	"any-hover":                      "css-at-media-hover",
	"device-pixel-ratio":             "css-at-media-device-pixel-ratio",
	"hover":                          "css-at-media-hover",
	"min-device-pixel-ratio":         "css-at-media-device-pixel-ratio",
	"orientation":                    "css-at-media-orientation",
	"prefers-color-scheme":           "css-at-media-prefers-color-scheme",
	"prefers-reduced-motion":         "css-at-media-prefers-reduced-motion",
	"-webkit-device-pixel-ratio":     "css-at-media-device-pixel-ratio",
	"-webkit-min-device-pixel-ratio": "css-at-media-device-pixel-ratio",
}

// cssPseudoClassRules maps lowercase pseudo-class names (without ':') to slugs.
var cssPseudoClassRules = map[string]string{
	"active":           "css-pseudo-class-active",
	"checked":          "css-pseudo-class-checked",
	"default":          "css-pseudo-class-default",
	"first-child":      "css-pseudo-class-first-child",
	"first-of-type":    "css-pseudo-class-first-of-type",
	"focus":            "css-pseudo-class-focus",
	"focus-visible":    "css-pseudo-class-focus-visible",
	"focus-within":     "css-pseudo-class-focus-within",
	"has":              "css-pseudo-class-has",
	"hover":            "css-pseudo-class-hover",
	"lang":             "css-pseudo-class-lang",
	"last-child":       "css-pseudo-class-last-child",
	"last-of-type":     "css-pseudo-class-last-of-type",
	"link":             "css-pseudo-class-link",
	"not":              "css-pseudo-class-not",
	"nth-child":        "css-pseudo-class-nth-child",
	"nth-last-child":   "css-pseudo-class-nth-last-child",
	"nth-last-of-type": "css-pseudo-class-nth-last-of-type",
	"nth-of-type":      "css-pseudo-class-nth-of-type",
	"only-child":       "css-pseudo-class-only-child",
	"only-of-type":     "css-pseudo-class-only-of-type",
	"target":           "css-pseudo-class-target",
	"visited":          "css-pseudo-class-visited",
}

// cssPseudoElementRules maps lowercase pseudo-element names (without '::') to slugs.
var cssPseudoElementRules = map[string]string{
	"after":        "css-pseudo-element-after",
	"before":       "css-pseudo-element-before",
	"first-letter": "css-pseudo-element-first-letter",
	"first-line":   "css-pseudo-element-first-line",
	"marker":       "css-pseudo-element-marker",
	"placeholder":  "css-pseudo-element-placeholder",
}

// slugCSSSystemUI matches caniemail.com's upstream slug spelling verbatim.
const slugCSSSystemUI = "css-sytem-ui" //nolint:misspell // upstream caniemail.com slug typo

// cssSelectorSpecialSlugs lists selector/combinator slugs emitted by the CSS parser.
var cssSelectorSpecialSlugs = []string{
	"css-selector-adjacent-sibling",
	"css-selector-attribute",
	"css-selector-chaining",
	"css-selector-child",
	"css-selector-class",
	"css-selector-descendant",
	"css-selector-general-sibling",
	"css-selector-grouping",
	"css-selector-id",
	"css-selector-type",
	"css-selector-universal",
	"css-comments",
	"css-important",
	"css-nesting",
	"css-variables",
	"css-intrinsic-size",
	"css-unit-initial",
	slugCSSSystemUI,
}

// cssPropertyRules maps lowercase CSS property names to caniemail feature slugs.
var cssPropertyRules = map[string]string{
	"accent-color":               "css-accent-color",
	"align-items":                "css-align-items",
	"animation":                  "css-animation",
	"animation-delay":            "css-animation",
	"animation-direction":        "css-animation",
	"animation-duration":         "css-animation",
	"animation-fill-mode":        "css-animation",
	"animation-iteration-count":  "css-animation",
	"animation-name":             "css-animation",
	"animation-timing-function":  "css-animation",
	"aspect-ratio":               "css-aspect-ratio",
	"backdrop-filter":            "css-backdrop-filter",
	"-webkit-backdrop-filter":    "css-backdrop-filter",
	"background":                 "css-background",
	"background-blend-mode":      "css-background-blend-mode",
	"background-clip":            "css-background-clip",
	"-webkit-background-clip":    "css-background-clip",
	"background-color":           "css-background-color",
	"background-image":           "css-background-image",
	"background-origin":          "css-background-origin",
	"background-position":        "css-background-position",
	"background-repeat":          "css-background-repeat",
	"background-size":            "css-background-size",
	"block-size":                 "css-block-inline-size",
	"border":                     "css-border",
	"border-block":               "css-border-inline-block",
	"border-block-color":         "css-border-inline-block-longhand",
	"border-block-end":           "css-border-inline-block-individual",
	"border-block-end-color":     "css-border-inline-block-longhand",
	"border-block-end-style":     "css-border-inline-block-longhand",
	"border-block-end-width":     "css-border-inline-block-longhand",
	"border-block-start":         "css-border-inline-block-individual",
	"border-block-start-color":   "css-border-inline-block-longhand",
	"border-block-start-style":   "css-border-inline-block-longhand",
	"border-block-start-width":   "css-border-inline-block-longhand",
	"border-block-style":         "css-border-inline-block-longhand",
	"border-block-width":         "css-border-inline-block-longhand",
	"border-bottom":              "css-border",
	"border-bottom-left-radius":  "css-border-radius",
	"border-bottom-right-radius": "css-border-radius",
	"border-collapse":            "css-border-collapse",
	"border-end-end-radius":      "css-border-radius-logical",
	"border-end-start-radius":    "css-border-radius-logical",
	"border-image":               "css-border-image",
	"border-image-source":        "css-border-image",
	"border-inline":              "css-border-inline-block",
	"border-inline-color":        "css-border-inline-block-longhand",
	"border-inline-end":          "css-border-inline-block-individual",
	"border-inline-end-color":    "css-border-inline-block-longhand",
	"border-inline-end-style":    "css-border-inline-block-longhand",
	"border-inline-end-width":    "css-border-inline-block-longhand",
	"border-inline-start":        "css-border-inline-block-individual",
	"border-inline-start-color":  "css-border-inline-block-longhand",
	"border-inline-start-style":  "css-border-inline-block-longhand",
	"border-inline-start-width":  "css-border-inline-block-longhand",
	"border-inline-style":        "css-border-inline-block-longhand",
	"border-inline-width":        "css-border-inline-block-longhand",
	"border-left":                "css-border",
	"border-radius":              "css-border-radius",
	"border-right":               "css-border",
	"border-spacing":             "css-border-spacing",
	"border-start-end-radius":    "css-border-radius-logical",
	"border-start-start-radius":  "css-border-radius-logical",
	"border-top":                 "css-border",
	"border-top-left-radius":     "css-border-radius",
	"border-top-right-radius":    "css-border-radius",
	"bottom":                     "css-left-right-top-bottom",
	"box-shadow":                 "css-box-shadow",
	"box-sizing":                 "css-box-sizing",
	"caption-side":               "css-caption-side",
	"clear":                      "css-clear",
	"clip-path":                  "css-clip-path",
	"-webkit-clip-path":          "css-clip-path",
	"color-scheme":               "css-color-scheme",
	"column-count":               "css-column-count",
	"column-fill":                "css-column-layout-properties",
	"column-gap":                 "css-gap",
	"column-rule":                "css-column-layout-properties",
	"column-span":                "css-column-layout-properties",
	"column-width":               "css-column-layout-properties",
	"columns":                    "css-column-layout-properties",
	"cursor":                     "css-cursor",
	"direction":                  "css-direction",
	"display":                    "css-display",
	"empty-cells":                "css-empty-cells",
	"filter":                     "css-filter",
	"-webkit-filter":             "css-filter",
	"float":                      "css-float",
	"font":                       "css-font",
	"font-kerning":               "css-font-kerning",
	"font-size":                  "css-font-size",
	"font-size-adjust":           "css-font-size-adjust",
	"font-stretch":               "css-font-stretch",
	"font-weight":                "css-font-weight",
	"gap":                        "css-gap",
	"grid-template":              "css-grid-template",
	"grid-template-areas":        "css-grid-template",
	"grid-template-columns":      "css-grid-template",
	"grid-template-rows":         "css-grid-template",
	"height":                     "css-height",
	"hyphenate-character":        "css-hyphenate-character",
	"hyphenate-limit-chars":      "css-hyphenate-limit-chars",
	"hyphens":                    "css-hyphens",
	"-webkit-hyphens":            "css-hyphens",
	"inline-size":                "css-inline-size",
	"inset":                      "css-inset",
	"inset-block":                "css-inset",
	"inset-inline":               "css-inset",
	"justify-content":            "css-justify-content",
	"left":                       "css-left-right-top-bottom",
	"letter-spacing":             "css-letter-spacing",
	"line-height":                "css-line-height",
	"list-style":                 "css-list-style",
	"list-style-image":           "css-list-style-image",
	"list-style-position":        "css-list-style-position",
	"list-style-type":            "css-list-style-type",
	"margin":                     "css-margin",
	"margin-block":               "css-margin-inline-block",
	"margin-block-end":           "css-margin-block-start-end",
	"margin-block-start":         "css-margin-block-start-end",
	"margin-bottom":              "css-margin",
	"margin-inline":              "css-margin-inline-block",
	"margin-inline-end":          "css-margin-inline-start-end",
	"margin-inline-start":        "css-margin-inline-start-end",
	"margin-left":                "css-margin",
	"margin-right":               "css-margin",
	"margin-top":                 "css-margin",
	"mask-image":                 "css-mask-image",
	"-webkit-mask-image":         "css-mask-image",
	"max-block-size":             "css-max-block-size",
	"max-height":                 "css-max-height",
	"max-inline-size":            "css-max-inline-size",
	"max-width":                  "css-max-width",
	"min-block-size":             "css-min-block-size",
	"min-height":                 "css-min-height",
	"min-inline-size":            "css-min-inline-size",
	"min-width":                  "css-min-width",
	"mix-blend-mode":             "css-mix-blend-mode",
	"object-fit":                 "css-object-fit",
	"object-position":            "css-object-position",
	"opacity":                    "css-opacity",
	"orphans":                    "css-orphans",
	"outline":                    "css-outline",
	"outline-offset":             "css-outline-offset",
	"overflow":                   "css-overflow",
	"overflow-wrap":              "css-overflow-wrap",
	"overflow-x":                 "css-overflow",
	"overflow-y":                 "css-overflow",
	"padding":                    "css-padding",
	"padding-block":              "css-padding-inline-block",
	"padding-block-end":          "css-padding-block-start-end",
	"padding-block-start":        "css-padding-block-start-end",
	"padding-bottom":             "css-padding",
	"padding-inline":             "css-padding-inline-block",
	"padding-inline-end":         "css-padding-inline-start-end",
	"padding-inline-start":       "css-padding-inline-start-end",
	"padding-left":               "css-padding",
	"padding-right":              "css-padding",
	"padding-top":                "css-padding",
	"position":                   "css-position",
	"resize":                     "css-resize",
	"right":                      "css-left-right-top-bottom",
	"row-gap":                    "css-gap",
	"scroll-snap-align":          "css-scroll-snap",
	"scroll-snap-stop":           "css-scroll-snap",
	"scroll-snap-type":           "css-scroll-snap",
	"shape-margin":               "css-shape-margin",
	"shape-outside":              "css-shape-outside",
	"tab-size":                   "css-tab-size",
	"table-layout":               "css-table-layout",
	"text-align":                 "css-text-align",
	"text-align-last":            "css-text-align-last",
	"text-decoration":            "css-text-decoration",
	"text-decoration-color":      "css-text-decoration-color",
	"text-decoration-line":       "css-text-decoration-line",
	"text-decoration-skip-ink":   "css-text-decoration-skip-ink",
	"text-decoration-style":      "css-text-decoration-style",
	"text-decoration-thickness":  "css-text-decoration-thickness",
	"text-emphasis":              "css-text-emphasis",
	"text-emphasis-color":        "css-text-emphasis",
	"text-emphasis-position":     "css-text-emphasis-position",
	"text-emphasis-style":        "css-text-emphasis",
	"text-indent":                "css-text-indent",
	"text-justify":               "css-text-justify",
	"text-orientation":           "css-text-orientation",
	"text-overflow":              "css-text-overflow",
	"text-shadow":                "css-text-shadow",
	"text-transform":             "css-text-transform",
	"text-underline-offset":      "css-text-underline-offset",
	"text-underline-position":    "css-text-underline-position",
	"text-wrap":                  "css-text-wrap",
	"top":                        "css-left-right-top-bottom",
	"transform":                  "css-transform",
	"-webkit-transform":          "css-transform",
	"transition":                 "css-transition",
	"transition-delay":           "css-transition",
	"transition-duration":        "css-transition",
	"transition-property":        "css-transition",
	"transition-timing-function": "css-transition",
	"user-select":                "css-user-select",
	"-webkit-user-select":        "css-user-select",
	"vertical-align":             "css-vertical-align",
	"visibility":                 "css-visibility",
	"white-space":                "css-white-space",
	"white-space-collapse":       "css-white-space-collapse",
	"widows":                     "css-widows",
	"width":                      "css-width",
	"word-break":                 "css-word-break",
	"word-spacing":               "css-word-spacing",
	"word-wrap":                  "css-word-wrap",
	"writing-mode":               "css-writing-mode",
	"z-index":                    "css-z-index",
}

// cssPropertyValueRules maps (property -> lowercase keyword value) to specific caniemail slugs.
// When a property-value rule matches, it takes precedence over the generic property rule
// (e.g. "display: flex" emits "css-display-flex" instead of generic "css-display").
var cssPropertyValueRules = map[string]map[string]string{
	"display": {
		"flex":        "css-display-flex",
		"inline-flex": "css-display-flex",
		"grid":        "css-display-grid",
		"inline-grid": "css-display-grid",
		"none":        "css-display-none",
	},
	"flex-direction": {
		"column":         "css-flex-direction",
		"column-reverse": "css-flex-direction",
	},
	"flex-wrap": {
		"wrap":         "css-flex-wrap",
		"wrap-reverse": "css-flex-wrap",
	},
}

// cssFunctionRules maps lowercase CSS function names (without '(') to caniemail slugs.
var cssFunctionRules = map[string]string{
	"calc":                      "css-unit-calc",
	"clamp":                     "css-function-clamp",
	"conic-gradient":            "css-conic-gradient",
	"lab":                       "css-modern-color",
	"lch":                       "css-modern-color",
	"light-dark":                "css-function-light-dark",
	"linear-gradient":           "css-linear-gradient",
	"max":                       "css-function-max",
	"min":                       "css-function-min",
	"oklab":                     "css-modern-color",
	"oklch":                     "css-modern-color",
	"radial-gradient":           "css-radial-gradient",
	"repeating-conic-gradient":  "css-conic-gradient",
	"repeating-linear-gradient": "css-linear-gradient",
	"repeating-radial-gradient": "css-radial-gradient",
	"rgb":                       "css-rgb",
	"rgba":                      "css-rgba",
	"var":                       "css-variables",
}

// cssUnitRules maps lowercase CSS numeric unit suffixes to caniemail slugs.
var cssUnitRules = map[string]string{
	"%":    "css-unit-percent",
	"ch":   "css-unit-ch",
	"cm":   "css-unit-cm",
	"em":   "css-unit-em",
	"ex":   "css-unit-ex",
	"in":   "css-unit-in",
	"mm":   "css-unit-mm",
	"pc":   "css-unit-pc",
	"pt":   "css-unit-pt",
	"px":   "css-unit-px",
	"rem":  "css-unit-rem",
	"vh":   "css-unit-vh",
	"vmax": "css-unit-vmax",
	"vmin": "css-unit-vmin",
	"vw":   "css-unit-vw",
}

// AllRuleSlugs returns a sorted, deduplicated slice of every caniemail feature
// slug referenced by the declarative rule registry.
func AllRuleSlugs() []string {
	seen := make(map[string]struct{}, 256)
	addMap := func(m map[string]string) {
		for _, slug := range m {
			seen[slug] = struct{}{}
		}
	}
	addSlice := func(s []string) {
		for _, slug := range s {
			seen[slug] = struct{}{}
		}
	}

	addMap(htmlElementRules)
	addMap(htmlInputTypeRules)
	addMap(htmlButtonTypeRules)
	addMap(htmlAttrRules)
	addSlice(htmlSpecialSlugs)
	addMap(imageExtRules)
	addMap(imageMIMERules)
	addSlice(imageSpecialSlugs)
	addMap(cssAtRuleRules)
	addMap(cssMediaFeatureRules)
	addMap(cssPseudoClassRules)
	addMap(cssPseudoElementRules)
	addSlice(cssSelectorSpecialSlugs)
	addMap(cssPropertyRules)
	for _, vm := range cssPropertyValueRules {
		addMap(vm)
	}
	addMap(cssFunctionRules)
	addMap(cssUnitRules)

	out := make([]string, 0, len(seen))
	for slug := range seen {
		out = append(out, slug)
	}
	sort.Strings(out)
	return out
}
