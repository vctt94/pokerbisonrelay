package ui

import "github.com/charmbracelet/lipgloss"

var (

	// Enhanced poker UI styles
	cardStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("255")).
			Foreground(lipgloss.Color("0")).
			Padding(0, 1).
			Margin(0, 1).
			Border(lipgloss.RoundedBorder())

	redCardStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("255")).
			Foreground(lipgloss.Color("196")).
			Padding(0, 1).
			Margin(0, 1).
			Border(lipgloss.RoundedBorder())

	playerBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			Padding(1, 2).
			Margin(0, 1)

	currentPlayerStyle = lipgloss.NewStyle().
				Border(lipgloss.ThickBorder()).
				BorderForeground(lipgloss.Color("46")).
				Padding(1, 2).
				Margin(0, 1).
				Background(lipgloss.Color("22"))

	yourPlayerStyle = lipgloss.NewStyle().
			Border(lipgloss.DoubleBorder()).
			BorderForeground(lipgloss.Color("39")).
			Padding(1, 2).
			Margin(0, 1).
			Background(lipgloss.Color("17"))

	foldedPlayerStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("241")).
				Foreground(lipgloss.Color("241")).
				Padding(1, 2).
				Margin(0, 1)

	potStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("22")).
			Foreground(lipgloss.Color("46")).
			Padding(1, 2).
			Margin(1).
			Border(lipgloss.ThickBorder()).
			BorderForeground(lipgloss.Color("46")).
			Align(lipgloss.Center).
			Bold(true)

	actionButtonStyle = lipgloss.NewStyle().
				Background(lipgloss.Color("17")).
				Foreground(lipgloss.Color("39")).
				Padding(0, 2).
				Margin(0, 1).
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("39"))

	selectedActionStyle = lipgloss.NewStyle().
				Background(lipgloss.Color("39")).
				Foreground(lipgloss.Color("0")).
				Padding(0, 2).
				Margin(0, 1).
				Border(lipgloss.ThickBorder()).
				BorderForeground(lipgloss.Color("46")).
				Bold(true)

	tableStyle = lipgloss.NewStyle().
			Border(lipgloss.DoubleBorder()).
			BorderForeground(lipgloss.Color("28")).
			Padding(2).
			Margin(1).
			Background(lipgloss.Color("22"))
)

// Common UI styles
var (
	FocusedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("205")).Bold(true)
	BlurredStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	TitleStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true).MarginLeft(2)
	HelpStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Margin(1, 0)
	ErrorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
)

var (
	focusedStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
	blurredStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	titleStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true).MarginLeft(2)
	gameInfoStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("140")).MarginTop(1)
	helpStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Margin(1, 0)
)

// Card styles
var (
	CardStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("255")).
			Foreground(lipgloss.Color("0")).
			Padding(0, 1).
			Margin(0, 1).
			Border(lipgloss.RoundedBorder())

	RedCardStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("255")).
			Foreground(lipgloss.Color("196")).
			Padding(0, 1).
			Margin(0, 1).
			Border(lipgloss.RoundedBorder())
)

// Player styles
var (
	PlayerBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			Padding(1, 2).
			Margin(0, 1)

	CurrentPlayerStyle = lipgloss.NewStyle().
				Border(lipgloss.ThickBorder()).
				BorderForeground(lipgloss.Color("46")).
				Padding(1, 2).
				Margin(0, 1).
				Background(lipgloss.Color("22"))

	YourPlayerStyle = lipgloss.NewStyle().
			Border(lipgloss.DoubleBorder()).
			BorderForeground(lipgloss.Color("39")).
			Padding(1, 2).
			Margin(0, 1).
			Background(lipgloss.Color("17"))

	FoldedPlayerStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("241")).
				Foreground(lipgloss.Color("241")).
				Padding(1, 2).
				Margin(0, 1)
)

// Game elements styles
var (
	PotStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("22")).
			Foreground(lipgloss.Color("46")).
			Padding(1, 2).
			Margin(1).
			Border(lipgloss.ThickBorder()).
			BorderForeground(lipgloss.Color("46")).
			Align(lipgloss.Center).
			Bold(true)

	ActionButtonStyle = lipgloss.NewStyle().
				Background(lipgloss.Color("17")).
				Foreground(lipgloss.Color("39")).
				Padding(0, 2).
				Margin(0, 1).
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("39"))

	SelectedActionStyle = lipgloss.NewStyle().
				Background(lipgloss.Color("39")).
				Foreground(lipgloss.Color("0")).
				Padding(0, 2).
				Margin(0, 1).
				Border(lipgloss.ThickBorder()).
				BorderForeground(lipgloss.Color("46")).
				Bold(true)

	TableStyle = lipgloss.NewStyle().
			Border(lipgloss.DoubleBorder()).
			BorderForeground(lipgloss.Color("28")).
			Padding(2).
			Margin(1).
			Background(lipgloss.Color("22"))
)
