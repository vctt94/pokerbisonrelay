package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/vctt94/poker-bisonrelay/pkg/rpc/grpc/pokerrpc"
)

// Renderer handles all rendering of UI screens and game elements
type Renderer struct {
	ui *PokerUI
}

// RenderMainMenu renders the main menu screen
func (r *Renderer) RenderMainMenu() string {
	var s string
	s += TitleStyle.Render("Poker Client - Main Menu") + "\n\n"
	s += fmt.Sprintf("Client ID: %s\n", r.ui.clientID)

	// Display cached balance in DCR
	if r.ui.balance > 0 {
		s += fmt.Sprintf("Account Balance: %.8f DCR\n", float64(r.ui.balance)/1e8)
	} else {
		s += "Account Balance: (loading...)\n"
	}

	// Show current table info if player is at a table
	currentTableID := r.ui.pc.GetCurrentTableID()
	if currentTableID != "" {
		s += fmt.Sprintf("Current Table: %s (Phase: %s)\n", currentTableID, r.ui.gamePhase.String())
	}
	s += "\n"

	options := r.ui.getMainMenuOptions()
	for i, option := range options {
		if i == r.ui.selectedItem {
			s += FocusedStyle.Render(fmt.Sprintf("> %s", option)) + "\n"
		} else {
			s += BlurredStyle.Render(fmt.Sprintf("  %s", option)) + "\n"
		}
	}

	return s
}

// RenderTableList renders the table list screen with compact styling to show more tables
func (r *Renderer) RenderTableList() string {
	var s string
	s += TitleStyle.Render("Available Tables") + "\n\n"

	if len(r.ui.tables) == 0 {
		s += BlurredStyle.Render("No tables available.") + "\n"
	} else {
		// Add header for better organization
		s += TitleStyle.Render("SELECT A TABLE TO JOIN") + "\n\n"

		for i, table := range r.ui.tables {
			isSelected := i == r.ui.selectedTable

			// Create more compact table info
			tableID := table.Id
			if len(tableID) > 20 {
				tableID = tableID[:17] + "..."
			}

			// Add status indicators for better information
			var status string
			if table.GameStarted {
				status = "PLAYING"
			} else if table.AllPlayersReady {
				status = "STARTING"
			} else if table.CurrentPlayers >= table.MinPlayers {
				status = "WAITING"
			} else {
				status = "OPEN"
			}

			// Compact single-line format with enhanced information
			tableInfo := fmt.Sprintf("%s | %s | Players: %d/%d | Blinds: %d/%d",
				status,
				tableID,
				table.CurrentPlayers,
				table.MaxPlayers,
				table.SmallBlind,
				table.BigBlind)

			// Add selection indicator and styling
			if isSelected {
				s += FocusedStyle.Render("> "+tableInfo) + "\n"
			} else {
				s += BlurredStyle.Render("  "+tableInfo) + "\n"
			}
		}

		// Add pagination info if there are many tables
		if len(r.ui.tables) > 10 {
			s += "\n" + BlurredStyle.Render(fmt.Sprintf("Showing %d tables (use ↑↓ to scroll)", len(r.ui.tables))) + "\n"
		}
	}

	s += "\n" + HelpStyle.Render("Press Enter to join selected table, 'r' to refresh, or 'q' to go back")
	return s
}

// RenderCreateTable renders the create table form screen
func (r *Renderer) RenderCreateTable() string {
	var s string
	s += TitleStyle.Render("Create New Table") + "\n\n"

	fields := []struct {
		label string
		value string
	}{
		{"Small Blind", r.ui.smallBlind},
		{"Big Blind", r.ui.bigBlind},
		{"Required Players", r.ui.requiredPlayers},
		{"Buy In", r.ui.buyIn},
		{"Min Balance", r.ui.minBalance},
		{"Starting Chips", r.ui.startingChips},
	}

	for i, field := range fields {
		style := BlurredStyle
		if i == r.ui.selectedFormField {
			style = FocusedStyle
		}
		s += style.Render(fmt.Sprintf("%s %s: %s",
			func() string {
				if i == r.ui.selectedFormField {
					return ">"
				}
				return " "
			}(),
			field.label,
			field.value,
		)) + "\n"
	}
	s += "\n" + HelpStyle.Render("Use arrow keys to navigate, type to edit, Enter to create table")
	return s
}

// RenderJoinTable renders the join table screen
func (r *Renderer) RenderJoinTable() string {
	var s string
	s += TitleStyle.Render("Join Table") + "\n\n"
	s += FocusedStyle.Render(fmt.Sprintf("Table ID: %s", r.ui.tableIdInput)) + "\n\n"
	s += HelpStyle.Render("Enter table ID and press Enter to join")
	return s
}

// RenderGameLobby renders the game lobby screen
func (r *Renderer) RenderGameLobby() string {
	var s string
	s += TitleStyle.Render(fmt.Sprintf("Game Lobby - Table %s", r.ui.pc.GetCurrentTableID())) + "\n\n"

	// Show client ID
	s += fmt.Sprintf("Client ID: %s\n", r.ui.clientID)

	// Display cached balance in DCR
	if r.ui.balance > 0 {
		s += fmt.Sprintf("Account Balance: %.8f DCR\n\n", float64(r.ui.balance)/1e8)
	} else {
		s += "Account Balance: (loading...)\n\n"
	}

	// Show table information if we have game update data
	if len(r.ui.players) > 0 {
		s += "Table Status:\n"
		s += fmt.Sprintf("Players: %d/%d (required to start)\n", r.ui.playersJoined, r.ui.playersRequired)
		s += fmt.Sprintf("Game Phase: %s\n", r.ui.gamePhase.String())
		if r.ui.pot > 0 {
			s += fmt.Sprintf("Pot: %d chips\n", r.ui.pot)
		}
		s += "\n"

		s += "Players at table:\n"
		for _, player := range r.ui.players {
			readyStatus := ""
			if player.IsReady {
				readyStatus = " ✅"
			} else {
				readyStatus = " ⏳"
			}

			currentPlayerIndicator := ""
			if player.Id == r.ui.clientID {
				currentPlayerIndicator = " (You)"
			}

			s += fmt.Sprintf("  %s: %d chips%s%s\n",
				player.Id, player.Balance, readyStatus, currentPlayerIndicator)
		}
		s += "\n"
	} else {
		s += "Loading table information...\n\n"
	}

	options := r.ui.getGameLobbyOptions()
	for i, option := range options {
		if i == r.ui.selectedItem {
			s += FocusedStyle.Render(fmt.Sprintf("> %s", option)) + "\n"
		} else {
			s += BlurredStyle.Render(fmt.Sprintf("  %s", option)) + "\n"
		}
	}

	return s
}

// RenderBetInput renders the bet input screen
func (r *Renderer) RenderBetInput() string {
	var s string
	s += TitleStyle.Render("Enter Bet Amount") + "\n\n"

	// Show client ID
	s += fmt.Sprintf("Client ID: %s\n", r.ui.clientID)

	bigBlind := r.ui.GetCurrentTableBigBlind()
	currentBet := r.ui.currentBet

	// Find the current player's bet amount
	var playerCurrentBet int64 = 0
	for _, player := range r.ui.players {
		if player.Id == r.ui.clientID {
			playerCurrentBet = player.CurrentBet
			break
		}
	}

	if currentBet == 0 {
		s += "No current bet\n"
		s += fmt.Sprintf("Minimum bet (big blind): %d\n\n", bigBlind)
	} else {
		s += fmt.Sprintf("Current bet to call: %d\n", currentBet)
		if playerCurrentBet < currentBet {
			// Player has a bet to call
			callAmount := currentBet - playerCurrentBet
			s += fmt.Sprintf("Amount to call: %d (you have %d bet, need %d more)\n", callAmount, playerCurrentBet, callAmount)
		}
		s += fmt.Sprintf("Minimum to call: %d\n", currentBet)
		s += fmt.Sprintf("Minimum to raise: %d\n\n", currentBet+bigBlind)
	}

	s += FocusedStyle.Render(fmt.Sprintf("Bet Amount: %s", r.ui.betAmount)) + "\n\n"
	s += HelpStyle.Render("Type amount and press Enter to bet, or 'q' to cancel")
	return s
}

// RenderActiveGame creates an enhanced poker table visualization
func (r *Renderer) RenderActiveGame() string {
	var s string

	// Game title with icon
	s += TitleStyle.Render(fmt.Sprintf("🎰 Table %s", r.ui.pc.GetCurrentTableID())) + "\n"

	// Show client ID
	s += fmt.Sprintf("Client ID: %s\n", r.ui.clientID)

	// COMMUNITY CARDS
	s += r.renderCommunityCardsSection() + "\n"

	// YOUR CARDS
	s += r.renderYourCardsAndGameInfo() + "\n"

	// SHOWDOWN RESULTS (if in showdown phase)
	if r.ui.gamePhase == pokerrpc.GamePhase_SHOWDOWN {
		s += r.renderShowdownResults() + "\n"
	}

	// Player information
	s += r.renderPlayersCompact() + "\n"

	// Action buttons for current player or waiting/leave options
	actionButtons := r.renderActionButtons()
	if actionButtons != "" {
		s += actionButtons
	} else {
		// Show appropriate options when not player's turn
		if r.ui.gamePhase == pokerrpc.GamePhase_SHOWDOWN {
			s += HelpStyle.Render("🏆 SHOWDOWN") + " | "
		} else if r.ui.currentPlayerID == "" {
			s += HelpStyle.Render("⏳ STARTING") + " | "
		} else if !isPlayerTurn(r.ui.currentPlayerID, r.ui.clientID) {
			// Find the current player's name for display
			currentPlayerName := r.ui.currentPlayerID
			if len(currentPlayerName) > 8 {
				currentPlayerName = currentPlayerName[:8] + "..."
			}
			s += HelpStyle.Render(fmt.Sprintf("⏰ Waiting for %s", currentPlayerName)) + " | "
		}

		// Always show leave table option when not actively playing
		s += BlurredStyle.Render("🚪 Leave Table")
	}

	s += "\n" + HelpStyle.Render("Arrow keys to navigate, Enter to select, 'q' to go back")
	return s
}

// renderCommunityCardsSection creates a clear, prominent display of community cards
func (r *Renderer) renderCommunityCardsSection() string {
	var s string

	// Clean header with subtle icon
	s += lipgloss.NewStyle().
		Foreground(lipgloss.Color("255")).
		Bold(true).
		Background(lipgloss.Color("22")).
		Padding(0, 2).
		Render("🃏 COMMUNITY CARDS") + "\n"

	// Cards display
	var cardElements []string

	switch r.ui.gamePhase {
	case pokerrpc.GamePhase_WAITING, pokerrpc.GamePhase_PRE_FLOP:
		// Show placeholders
		for i := 0; i < 5; i++ {
			cardElements = append(cardElements, CardStyle.Render("  "))
		}
	case pokerrpc.GamePhase_SHOWDOWN:
		// During showdown, show all dealt community cards without placeholders
		for _, card := range r.ui.communityCards {
			cardDisplay := r.formatCard(card)
			var styledCard string
			if isRedSuit(card.Suit) {
				styledCard = RedCardStyle.Render(cardDisplay)
			} else {
				styledCard = CardStyle.Render(cardDisplay)
			}
			cardElements = append(cardElements, styledCard)
		}
	default:
		// Add dealt community cards
		for _, card := range r.ui.communityCards {
			cardDisplay := r.formatCard(card)
			var styledCard string
			if isRedSuit(card.Suit) {
				styledCard = RedCardStyle.Render(cardDisplay)
			} else {
				styledCard = CardStyle.Render(cardDisplay)
			}
			cardElements = append(cardElements, styledCard)
		}

		// Add placeholders for remaining cards
		for i := len(r.ui.communityCards); i < 5; i++ {
			cardElements = append(cardElements, CardStyle.Render("  "))
		}
	}

	// Display cards centered
	cardsDisplay := strings.Join(cardElements, " ")
	s += lipgloss.NewStyle().
		Align(lipgloss.Center).
		Render(cardsDisplay) + "\n"

	// Phase indicator - cleaner with minimal icons
	var phaseText string
	switch r.ui.gamePhase {
	case pokerrpc.GamePhase_WAITING:
		phaseText = "⏳ WAITING FOR PLAYERS"
	case pokerrpc.GamePhase_PRE_FLOP:
		phaseText = "🎯 PRE-FLOP"
	case pokerrpc.GamePhase_FLOP:
		phaseText = "FLOP"
	case pokerrpc.GamePhase_TURN:
		phaseText = "TURN"
	case pokerrpc.GamePhase_RIVER:
		phaseText = "RIVER"
	case pokerrpc.GamePhase_SHOWDOWN:
		phaseText = "🏆 SHOWDOWN"
	default:
		phaseText = r.ui.gamePhase.String()
	}

	s += lipgloss.NewStyle().
		Foreground(lipgloss.Color("214")).
		Bold(true).
		Align(lipgloss.Center).
		Render(phaseText)

	return s
}

// renderYourCardsAndGameInfo creates a compact display combining player cards and game info
func (r *Renderer) renderYourCardsAndGameInfo() string {
	var s string

	// Your cards header - clean and prominent with subtle icon
	s += lipgloss.NewStyle().
		Foreground(lipgloss.Color("255")).
		Bold(true).
		Background(lipgloss.Color("17")).
		Padding(0, 2).
		Render("🂠 YOUR HAND") + "\n"

	var cardElements []string

	if r.ui.gamePhase != pokerrpc.GamePhase_WAITING {
		// Find player's cards
		var playerCards []*pokerrpc.Card
		for _, player := range r.ui.players {
			if player.Id == r.ui.clientID {
				playerCards = player.Hand
				break
			}
		}

		if len(playerCards) > 0 {
			for _, card := range playerCards {
				cardDisplay := r.formatCard(card)
				var styledCard string
				if isRedSuit(card.Suit) {
					styledCard = RedCardStyle.Render(cardDisplay)
				} else {
					styledCard = CardStyle.Render(cardDisplay)
				}
				cardElements = append(cardElements, styledCard)
			}
		} else {
			cardElements = []string{CardStyle.Render("  "), CardStyle.Render("  ")}
		}
	} else {
		cardElements = []string{CardStyle.Render("  "), CardStyle.Render("  ")}
	}

	cardsDisplay := strings.Join(cardElements, " ")
	s += lipgloss.NewStyle().
		Align(lipgloss.Center).
		Render(cardsDisplay)

	// Game info - cleaner format with chip icon
	potDisplay := fmt.Sprintf("💰 Pot: %d", r.ui.pot)
	if r.ui.currentBet > 0 {
		potDisplay += fmt.Sprintf(" | Current Bet: %d", r.ui.currentBet)
	}

	s += " | " + lipgloss.NewStyle().
		Foreground(lipgloss.Color("140")).
		Render(potDisplay)

	return s
}

// renderPlayersAroundTable creates a visual representation of players around a poker table
func (r *Renderer) renderPlayersAroundTable() string {
	if len(r.ui.players) == 0 {
		return ""
	}

	var result string
	result += TitleStyle.Render("👥 Players at Table 👥") + "\n\n"

	// Arrange players in a visual table layout
	for i, player := range r.ui.players {
		playerInfo := r.formatPlayerInfo(player)

		// Choose style based on player state
		var style lipgloss.Style
		if player.Id == r.ui.clientID {
			style = YourPlayerStyle
		} else if player.Id == r.ui.currentPlayerID {
			style = CurrentPlayerStyle
		} else if player.Folded {
			style = FoldedPlayerStyle
		} else {
			style = PlayerBoxStyle
		}

		// Add position indicator
		position := fmt.Sprintf("Seat %d", i+1)
		fullPlayerInfo := fmt.Sprintf("%s\n%s", position, playerInfo)

		result += style.Render(fullPlayerInfo) + "\n"
	}

	return result
}

// formatPlayerInfo creates a clean formatted string for player information
func (r *Renderer) formatPlayerInfo(player *pokerrpc.Player) string {
	var info []string

	// Player ID (truncated for display)
	playerName := player.Id
	if len(playerName) > 12 {
		playerName = playerName[:12] + "..."
	}
	info = append(info, playerName)

	// Game chips balance
	info = append(info, fmt.Sprintf("Chips: %d", player.Balance))

	// Current bet
	if player.CurrentBet > 0 {
		info = append(info, fmt.Sprintf("Bet: %d", player.CurrentBet))
	}

	// Status indicators - clean and clear
	var status []string

	// Check if player is you
	if player.Id == r.ui.clientID {
		status = append(status, "YOU")
	}

	// Check if it's their turn (and they haven't folded)
	if player.Id == r.ui.currentPlayerID && !player.Folded {
		status = append(status, "ACTING")
	}

	// Only show folded if player has actually folded
	if player.Folded {
		status = append(status, "FOLDED")
	} else {
		// Player is active in the game
		if r.ui.gamePhase != pokerrpc.GamePhase_WAITING {
			status = append(status, "ACTIVE")
		} else {
			// In waiting phase, show ready status
			if player.IsReady {
				status = append(status, "READY")
			} else {
				status = append(status, "NOT READY")
			}
		}
	}

	if len(status) > 0 {
		info = append(info, fmt.Sprintf("[%s]", strings.Join(status, ", ")))
	}

	return strings.Join(info, " | ")
}

// renderActionButtons creates clear action buttons with better UX
func (r *Renderer) renderActionButtons() string {
	// Check if it's player's turn and they're in an active game phase
	if !isPlayerTurn(r.ui.currentPlayerID, r.ui.clientID) {
		return ""
	}

	// Only show action buttons during betting phases
	switch r.ui.gamePhase {
	case pokerrpc.GamePhase_PRE_FLOP, pokerrpc.GamePhase_FLOP, pokerrpc.GamePhase_TURN, pokerrpc.GamePhase_RIVER:
		// These are valid phases for player actions
	default:
		// In other phases (WAITING, SHOWDOWN), don't show action buttons
		return ""
	}

	var result string

	// Very clear turn indicator with icon
	result += lipgloss.NewStyle().
		Foreground(lipgloss.Color("255")).
		Bold(true).
		Background(lipgloss.Color("196")).
		Padding(0, 2).
		Render("⚡ YOUR TURN - CHOOSE ACTION") + "\n\n"

	// Clean action buttons with minimal icons
	options := r.ui.getActiveGameOptions()
	for i, option := range options {
		var buttonText string

		switch option {
		case "Check":
			buttonText = "✅ Check"
		case "Call":
			buttonText = "📞 Call"
		case "Bet":
			buttonText = "💸 Bet/Raise"
		case "Fold":
			buttonText = "❌ Fold"
		case "Leave Table":
			buttonText = "🚪 Leave Table"
		default:
			buttonText = option
		}

		if i == r.ui.selectedItem {
			result += FocusedStyle.Render(fmt.Sprintf("> %s", buttonText)) + "\n"
		} else {
			result += BlurredStyle.Render(fmt.Sprintf("  %s", buttonText)) + "\n"
		}
	}

	return result
}

// formatCard creates a clean visual representation of a playing card
func (r *Renderer) formatCard(card *pokerrpc.Card) string {
	if card == nil {
		return "  "
	}

	// Convert suit to symbol
	suitSymbol := getSuitSymbol(card.Suit)

	// Format value
	value := card.Value
	if value == "T" {
		value = "10"
	}

	return fmt.Sprintf("%s%s", value, suitSymbol)
}

// getSuitSymbol returns the appropriate symbol for a suit
func getSuitSymbol(suit string) string {
	// The suit is already the Unicode symbol, so just return it
	switch suit {
	case "♠":
		return "♠"
	case "♥":
		return "♥"
	case "♦":
		return "♦"
	case "♣":
		return "♣"
	default:
		return "?"
	}
}

// isRedSuit determines if a suit should be displayed in red
func isRedSuit(suit string) bool {
	return suit == "♥" || suit == "♦"
}

// renderPlayersCompact creates a clear representation of players with better turn indication
func (r *Renderer) renderPlayersCompact() string {
	if len(r.ui.players) == 0 {
		return HelpStyle.Render("No players")
	}

	var result strings.Builder
	result.WriteString(lipgloss.NewStyle().
		Foreground(lipgloss.Color("255")).
		Bold(true).
		Render("👥 PLAYERS") + "\n")

	var playerLines []string
	for _, player := range r.ui.players {
		// Player name (truncated reasonably)
		playerName := player.Id
		if len(playerName) > 12 {
			playerName = playerName[:12] + "..."
		}

		// Determine player status and styling
		var line string
		var style lipgloss.Style

		if player.Id == r.ui.clientID {
			// This is YOU - make it very clear
			style = lipgloss.NewStyle().
				Foreground(lipgloss.Color("39")).
				Bold(true).
				Background(lipgloss.Color("17"))
			line = fmt.Sprintf(" 🔵 YOU (%s) - Chips: %d", playerName, player.Balance)
		} else if player.Id == r.ui.currentPlayerID && !player.Folded {
			// Current player's turn - highlight clearly
			style = lipgloss.NewStyle().
				Foreground(lipgloss.Color("46")).
				Bold(true).
				Background(lipgloss.Color("22"))
			line = fmt.Sprintf(" ⏰ ACTING: %s - Chips: %d", playerName, player.Balance)
		} else if player.Folded {
			// Folded player - muted
			style = lipgloss.NewStyle().
				Foreground(lipgloss.Color("241"))
			line = fmt.Sprintf(" ❌ %s - FOLDED", playerName)
		} else {
			// Regular active player
			style = lipgloss.NewStyle().
				Foreground(lipgloss.Color("255"))
			line = fmt.Sprintf(" %s - Chips: %d", playerName, player.Balance)
		}

		// Add current bet if they have one
		if player.CurrentBet > 0 && !player.Folded {
			line += fmt.Sprintf(" (Bet: %d)", player.CurrentBet)
		}

		playerLines = append(playerLines, style.Render(line))
	}

	result.WriteString(strings.Join(playerLines, "\n"))
	return result.String()
}

// renderShowdownResults displays the showdown results with winner cards and hand information
func (r *Renderer) renderShowdownResults() string {
	if r.ui.gamePhase != pokerrpc.GamePhase_SHOWDOWN {
		return ""
	}

	var s string

	// Showdown header
	s += lipgloss.NewStyle().
		Foreground(lipgloss.Color("255")).
		Bold(true).
		Background(lipgloss.Color("202")).
		Padding(0, 2).
		Align(lipgloss.Center).
		Width(50).
		Render("🏆 SHOWDOWN RESULTS 🏆") + "\n\n"

	// Display all players' hands during showdown
	s += lipgloss.NewStyle().
		Foreground(lipgloss.Color("255")).
		Bold(true).
		Background(lipgloss.Color("22")).
		Padding(0, 1).
		Render("👥 ALL PLAYERS' HANDS") + "\n"

	for _, player := range r.ui.players {
		if player.Folded {
			continue // Skip folded players
		}

		playerName := player.Id
		if len(playerName) > 15 {
			playerName = playerName[:15] + "..."
		}

		// Player info line
		var playerLine string
		var style lipgloss.Style

		if player.Id == r.ui.clientID {
			style = lipgloss.NewStyle().
				Foreground(lipgloss.Color("39")).
				Bold(true)
			playerLine = fmt.Sprintf("🔵 YOU (%s)", playerName)
		} else {
			style = lipgloss.NewStyle().
				Foreground(lipgloss.Color("255"))
			playerLine = fmt.Sprintf("👤 %s", playerName)
		}

		s += style.Render(playerLine) + "\n"

		// Show player's hole cards (their original 2 cards)
		if len(player.Hand) > 0 {
			var cardElements []string
			for _, card := range player.Hand {
				cardDisplay := r.formatCard(card)
				var styledCard string
				if isRedSuit(card.Suit) {
					styledCard = RedCardStyle.Render(cardDisplay)
				} else {
					styledCard = CardStyle.Render(cardDisplay)
				}
				cardElements = append(cardElements, styledCard)
			}
			cardsDisplay := strings.Join(cardElements, " ")
			s += "   Hole Cards: " + cardsDisplay + "\n"
		}

		// Show hand description if available from server during showdown
		handDescription := "Evaluating..."
		if player.GetHandDescription() != "" {
			handDescription = player.GetHandDescription()
		}
		s += "   Hand: " + lipgloss.NewStyle().
			Foreground(lipgloss.Color("214")).
			Render(handDescription) + "\n\n"
	}

	// Display winners section
	if len(r.ui.winners) > 0 {
		s += lipgloss.NewStyle().
			Foreground(lipgloss.Color("255")).
			Bold(true).
			Background(lipgloss.Color("22")).
			Padding(0, 1).
			Render("🏆 WINNERS") + "\n"

		for _, winner := range r.ui.winners {
			winnerName := winner.PlayerId
			if len(winnerName) > 15 {
				winnerName = winnerName[:15] + "..."
			}

			var winnerStyle lipgloss.Style
			if winner.PlayerId == r.ui.clientID {
				winnerStyle = lipgloss.NewStyle().
					Foreground(lipgloss.Color("46")).
					Bold(true).
					Background(lipgloss.Color("22"))
			} else {
				winnerStyle = lipgloss.NewStyle().
					Foreground(lipgloss.Color("46")).
					Bold(true)
			}

			winnerLine := fmt.Sprintf("🎉 %s - Won %d chips", winnerName, winner.Winnings)
			s += winnerStyle.Render(winnerLine) + "\n"

			// Show winner's best 5-card hand
			if len(winner.BestHand) > 0 {
				var bestHandCards []string
				for _, card := range winner.BestHand {
					cardDisplay := r.formatCard(card)
					var styledCard string
					if isRedSuit(card.Suit) {
						styledCard = RedCardStyle.Render(cardDisplay)
					} else {
						styledCard = CardStyle.Render(cardDisplay)
					}
					bestHandCards = append(bestHandCards, styledCard)
				}
				bestHandDisplay := strings.Join(bestHandCards, " ")
				s += "   Best Hand: " + bestHandDisplay + "\n"
			}

			// Show hand rank
			if winner.HandRank != pokerrpc.HandRank_HIGH_CARD {
				rankText := winner.HandRank.String()
				s += "   Rank: " + lipgloss.NewStyle().
					Foreground(lipgloss.Color("214")).
					Bold(true).
					Render(rankText) + "\n"
			}
			s += "\n"
		}
	}

	return s
}
