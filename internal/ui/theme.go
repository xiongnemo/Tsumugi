package ui

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

type Theme struct {
	Border    tcell.Color
	Title     tcell.Color
	Accent    tcell.Color
	Error     tcell.Color
	Status    tcell.Color
	Dim       tcell.Color
	Selection tcell.Color
}

func DefaultTheme() Theme {
	return Theme{
		Border:    tcell.ColorDarkCyan,
		Title:     tcell.ColorWhite,
		Accent:    tcell.ColorLightCyan,
		Error:     tcell.ColorIndianRed,
		Status:    tcell.ColorBlue,
		Dim:       tcell.ColorGray,
		Selection: tcell.ColorDarkSlateGray,
	}
}

func ApplyTheme(theme Theme) {
	tview.Styles.PrimitiveBackgroundColor = tcell.ColorDefault
	tview.Styles.ContrastBackgroundColor = theme.Selection
	tview.Styles.PrimaryTextColor = theme.Title
	tview.Styles.SecondaryTextColor = theme.Dim
	tview.Styles.BorderColor = theme.Border
	tview.Styles.TitleColor = theme.Accent
}
