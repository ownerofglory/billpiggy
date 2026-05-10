package domain

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// Currency represents currency
type Currency struct {
	// Currency code, e.g. EUR
	Code string
	// Currency name, e.g. Euro
	Name string
}

// Money amount of money
type Money struct {
	Amount   decimal.Decimal
	Currency Currency
}

// Expense represents spending
type Expense struct {
	// Brief description of the spending
	Description string
	// Amount and currency information
	Money Money
	// Date and time of spending
	Timestamp time.Time
	// Category expense belong to
	Category Category
	// Associated expense tags
	Tags []Tag
}

// Category represents expense, e.g. "Transportation".
// Each expense belong to one category
// User owns their categories
type Category struct {
	Name string
}

const defaultR, defaultG, defaultB = 255, 255, 255
const defaultColorName = "white"

const defaultFontR, defaultFontG, defaultFontB = 255, 255, 255
const defaultFontColorName = "black"

// Color represents color values
type Color struct {
	// RGB-values
	r, g, b uint8
	// User-friendly color name
	Name string
}

// Hex Returns hex representation of the color
func (c Color) Hex() string {
	return fmt.Sprintf("#%02x%02x%02x", c.r, c.g, c.b)
}

// NewColorFromHex converts hex representation of color into [Color]
func NewColorFromHex(hex string) (Color, error) {
	hex = strings.TrimPrefix(strings.TrimSpace(hex), "#")

	if len(hex) != 6 {
		return Color{}, fmt.Errorf("invalid hex color length: %q", hex)
	}

	r, err := strconv.ParseUint(hex[0:2], 16, 8)
	if err != nil {
		return Color{}, fmt.Errorf("invalid red component: %w", err)
	}

	g, err := strconv.ParseUint(hex[2:4], 16, 8)
	if err != nil {
		return Color{}, fmt.Errorf("invalid green component: %w", err)
	}

	b, err := strconv.ParseUint(hex[4:6], 16, 8)
	if err != nil {
		return Color{}, fmt.Errorf("invalid blue component: %w", err)
	}

	return Color{
		r: uint8(r),
		g: uint8(g),
		b: uint8(b),
	}, nil
}

// Tag a tag a.k.a. label for an expense
// User owns their categories
type Tag struct {
	// Tag name
	Name string
	// Color value
	Color Color
	// Font color value
	FontColor Color
	// User
	User User
}

// NewTag Creates a new tag
func NewTag(name, colorHex, fontColorHex string) Tag {
	color, err := NewColorFromHex(colorHex)
	if err != nil {
		color = Color{
			r:    defaultR,
			g:    defaultG,
			b:    defaultB,
			Name: defaultColorName,
		}
	}
	fontColor, err := NewColorFromHex(fontColorHex)
	if err != nil {
		fontColor = Color{
			r:    defaultFontR,
			g:    defaultFontG,
			b:    defaultFontB,
			Name: defaultFontColorName,
		}
	}

	return Tag{
		Name:      name,
		Color:     color,
		FontColor: fontColor,
	}
}
