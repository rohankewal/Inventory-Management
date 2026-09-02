package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// Appearance is the user's theme preference.
type Appearance string

const (
	AppearanceSystem Appearance = "system"
	AppearanceLight  Appearance = "light"
	AppearanceDark   Appearance = "dark"
)

// Label is the human-readable name for a preference menu.
func (a Appearance) Label() string {
	switch a {
	case AppearanceLight:
		return "Light"
	case AppearanceDark:
		return "Dark"
	}
	return "Match system"
}

// Appearances lists the choices in display order.
var Appearances = []Appearance{AppearanceSystem, AppearanceLight, AppearanceDark}

// appTheme is the application's visual identity.
//
// Fyne's stock theme is fine for a utility, but an application someone stares
// at for eight hours needs a quieter background, stronger separators and more
// generous spacing than the default. Every value here is a deliberate choice
// rather than a tweak: dense business data needs contrast between the surface
// a table sits on and the surface behind it, which the default's single flat
// background does not provide.
type appTheme struct {
	// override forces a variant. Zero means follow the operating system.
	override    fyne.ThemeVariant
	hasOverride bool
}

func newTheme(appearance Appearance) *appTheme {
	t := &appTheme{}
	switch appearance {
	case AppearanceLight:
		t.override, t.hasOverride = theme.VariantLight, true
	case AppearanceDark:
		t.override, t.hasOverride = theme.VariantDark, true
	}
	return t
}

func (t *appTheme) variant(v fyne.ThemeVariant) fyne.ThemeVariant {
	if t.hasOverride {
		return t.override
	}
	return v
}

// Light palette. Backgrounds step from the page, through cards, to inputs, so
// that depth is readable without borders everywhere.
var lightColors = map[fyne.ThemeColorName]color.Color{
	theme.ColorNameBackground:          rgb(0xF6, 0xF7, 0xF9),
	theme.ColorNameForeground:          rgb(0x17, 0x1A, 0x22),
	theme.ColorNamePrimary:             rgb(0x4F, 0x46, 0xE5),
	theme.ColorNameHover:               rgba(0x4F, 0x46, 0xE5, 0x14),
	theme.ColorNamePressed:             rgba(0x4F, 0x46, 0xE5, 0x28),
	theme.ColorNameSelection:           rgba(0x4F, 0x46, 0xE5, 0x24),
	theme.ColorNameFocus:               rgba(0x4F, 0x46, 0xE5, 0x66),
	theme.ColorNameButton:              rgb(0xFF, 0xFF, 0xFF),
	theme.ColorNameDisabledButton:      rgb(0xEC, 0xEE, 0xF2),
	theme.ColorNameDisabled:            rgb(0x9B, 0xA2, 0xB0),
	theme.ColorNamePlaceHolder:         rgb(0x98, 0x9F, 0xAD),
	theme.ColorNameInputBackground:     rgb(0xFF, 0xFF, 0xFF),
	theme.ColorNameInputBorder:         rgb(0xD8, 0xDD, 0xE5),
	theme.ColorNameMenuBackground:      rgb(0xFF, 0xFF, 0xFF),
	theme.ColorNameOverlayBackground:   rgb(0xFF, 0xFF, 0xFF),
	theme.ColorNameHeaderBackground:    rgb(0xED, 0xEF, 0xF3),
	theme.ColorNameSeparator:           rgb(0xE1, 0xE5, 0xEC),
	theme.ColorNameScrollBar:           rgba(0x17, 0x1A, 0x22, 0x40),
	theme.ColorNameScrollBarBackground: rgba(0x17, 0x1A, 0x22, 0x0A),
	theme.ColorNameShadow:              rgba(0x0F, 0x14, 0x1F, 0x1F),
	theme.ColorNameSuccess:             rgb(0x04, 0x7A, 0x55),
	theme.ColorNameWarning:             rgb(0xB4, 0x54, 0x09),
	theme.ColorNameError:               rgb(0xC0, 0x25, 0x25),
	theme.ColorNameHyperlink:           rgb(0x4F, 0x46, 0xE5),
}

// Dark palette. Kept off pure black: a true-black background against white
// text is the fastest way to tire someone's eyes on a long shift.
var darkColors = map[fyne.ThemeColorName]color.Color{
	theme.ColorNameBackground:          rgb(0x12, 0x14, 0x1A),
	theme.ColorNameForeground:          rgb(0xE7, 0xEA, 0xF0),
	theme.ColorNamePrimary:             rgb(0x81, 0x8C, 0xF8),
	theme.ColorNameHover:               rgba(0x81, 0x8C, 0xF8, 0x1F),
	theme.ColorNamePressed:             rgba(0x81, 0x8C, 0xF8, 0x33),
	theme.ColorNameSelection:           rgba(0x81, 0x8C, 0xF8, 0x30),
	theme.ColorNameFocus:               rgba(0x81, 0x8C, 0xF8, 0x77),
	theme.ColorNameButton:              rgb(0x22, 0x26, 0x30),
	theme.ColorNameDisabledButton:      rgb(0x1B, 0x1E, 0x26),
	theme.ColorNameDisabled:            rgb(0x6B, 0x73, 0x84),
	theme.ColorNamePlaceHolder:         rgb(0x79, 0x81, 0x92),
	theme.ColorNameInputBackground:     rgb(0x1B, 0x1F, 0x27),
	theme.ColorNameInputBorder:         rgb(0x33, 0x39, 0x46),
	theme.ColorNameMenuBackground:      rgb(0x1B, 0x1F, 0x27),
	theme.ColorNameOverlayBackground:   rgb(0x1B, 0x1F, 0x27),
	theme.ColorNameHeaderBackground:    rgb(0x1E, 0x22, 0x2B),
	theme.ColorNameSeparator:           rgb(0x2A, 0x2F, 0x3A),
	theme.ColorNameScrollBar:           rgba(0xE7, 0xEA, 0xF0, 0x40),
	theme.ColorNameScrollBarBackground: rgba(0xE7, 0xEA, 0xF0, 0x0A),
	theme.ColorNameShadow:              rgba(0x00, 0x00, 0x00, 0x66),
	theme.ColorNameSuccess:             rgb(0x34, 0xD3, 0x99),
	theme.ColorNameWarning:             rgb(0xFB, 0xBF, 0x24),
	theme.ColorNameError:               rgb(0xF8, 0x71, 0x71),
	theme.ColorNameHyperlink:           rgb(0x93, 0x9E, 0xFF),
}

func (t *appTheme) Color(name fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	palette := lightColors
	if t.variant(v) == theme.VariantDark {
		palette = darkColors
	}
	if c, ok := palette[name]; ok {
		return c
	}
	return theme.DefaultTheme().Color(name, t.variant(v))
}

func (t *appTheme) Font(style fyne.TextStyle) fyne.Resource {
	return theme.DefaultTheme().Font(style)
}

func (t *appTheme) Icon(name fyne.ThemeIconName) fyne.Resource {
	return theme.DefaultTheme().Icon(name)
}

func (t *appTheme) Size(name fyne.ThemeSizeName) float32 {
	switch name {
	case theme.SizeNamePadding:
		return 5 // roomier than the default 4, which crowds dense tables
	case theme.SizeNameInnerPadding:
		return 9
	case theme.SizeNameText:
		return 13.5
	case theme.SizeNameCaptionText:
		return 11.5
	case theme.SizeNameHeadingText:
		return 22
	case theme.SizeNameSubHeadingText:
		return 16
	case theme.SizeNameSeparatorThickness:
		return 1
	case theme.SizeNameInputBorder:
		return 1
	case theme.SizeNameButtonRadius, theme.SizeNameInputRadius, theme.SizeNameSelectionRadius:
		return 6
	case theme.SizeNameCardRadius, theme.SizeNameDialogRadius, theme.SizeNamePopupRadius:
		return 10
	case theme.SizeNameScrollBar:
		return 11
	case theme.SizeNameScrollBarSmall:
		return 4
	}
	return theme.DefaultTheme().Size(name)
}

func rgb(r, g, b uint8) color.Color { return color.NRGBA{R: r, G: g, B: b, A: 0xFF} }

func rgba(r, g, b, a uint8) color.Color { return color.NRGBA{R: r, G: g, B: b, A: a} }

var _ fyne.Theme = (*appTheme)(nil)
