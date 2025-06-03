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
	s += TitleStyle.Render("🃏 Poker Client - Main Menu 🃏") + "\n\n"
	s += fmt.Sprintf("Client ID: %s\n", r.ui.clientID)

	// Get current balance from poker client
	if balance, err := r.ui.pc.GetBalance(r.ui.ctx); err == nil {
		s += fmt.Sprintf("💰 Balance: %d\n", balance)
	} else {
		s += "💰 Balance: (loading...)\n"
	}

	// Show current table info if player is at a table
	currentTableID := r.ui.pc.GetCurrentTableID()
	if currentTableID != "" {
		s += fmt.Sprintf("🎲 Current Table: %s (Phase: %s)\n", currentTableID, r.ui.gamePhase.String())
	}
	s += "\n"

	for i, option := range r.ui.menuOptions {
		if i == r.ui.selectedItem {
			s += FocusedStyle.Render(fmt.Sprintf("▶ %s", option)) + "\n"
		} else {
			s += BlurredStyle.Render(fmt.Sprintf("  %s", option)) + "\n"
		}
	}

	return s
}

// RenderTableList renders the table list screen with compact styling to show more tables
func (r *Renderer) RenderTableList() string {
	var s string
	s += TitleStyle.Render("🎯 Available Tables 🎯") + "\n\n"

	if len(r.ui.tables) == 0 {
		s += BlurredStyle.Render("No tables available.") + "\n"
	} else {
		// Add header for better organization
		s += TitleStyle.Render("📋 SELECT A TABLE TO JOIN") + "\n\n"

		for i, table := range r.ui.tables {
			isSelected := i == r.ui.selectedTable

			// Create more compact table info with icons
			tableID := table.Id
			if len(tableID) > 20 {
				tableID = tableID[:17] + "..."
			}

			// Add status indicators for better information
			var status string
			if table.GameStarted {
				status = "🎮" // Game in progress
			} else if table.AllPlayersReady {
				status = "⚡" // All ready, about to start
			} else if table.CurrentPlayers >= table.MinPlayers {
				status = "⏳" // Enough players, waiting for ready
			} else {
				status = "📍" // Waiting for more players
			}

			// Compact single-line format with enhanced information
			tableInfo := fmt.Sprintf("%s %s | 👥 %d/%d | 💸 %d/%d",
				status,
				tableID,
				table.CurrentPlayers,
				table.MaxPlayers,
				table.SmallBlind,
				table.BigBlind)

			// Add selection indicator and styling
			if isSelected {
				s += FocusedStyle.Render("▶ "+tableInfo) + "\n"
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
	s += TitleStyle.Render("🆕 Create New Table 🆕") + "\n\n"

	fields := []struct {
		label string
		value string
	}{
		{"💸 Small Blind", r.ui.smallBlind},
		{"💰 Big Blind", r.ui.bigBlind},
		{"👥 Required Players", r.ui.requiredPlayers},
		{"🎫 Buy In", r.ui.buyIn},
		{"💵 Min Balance", r.ui.minBalance},
	}

	for i, field := range fields {
		style := BlurredStyle
		if i == r.ui.selectedFormField {
			style = FocusedStyle
		}
		s += style.Render(fmt.Sprintf("%s %s: %s",
			func() string {
				if i == r.ui.selectedFormField {
					return "▶"
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
	s += TitleStyle.Render("🎯 Join Table 🎯") + "\n\n"
	s += FocusedStyle.Render(fmt.Sprintf("🎲 Table ID: %s", r.ui.tableIdInput)) + "\n\n"
	s += HelpStyle.Render("Enter table ID and press Enter to join")
	return s
}

// RenderGameLobby renders the game lobby screen
func (r *Renderer) RenderGameLobby() string {
	var s string
	s += TitleStyle.Render(fmt.Sprintf("🎰 Game Lobby - Table %s 🎰", r.ui.pc.GetCurrentTableID())) + "\n\n"

	// Get current balance from poker client
	if balance, err := r.ui.pc.GetBalance(r.ui.ctx); err == nil {
		s += fmt.Sprintf("💰 Balance: %d\n\n", balance)
	} else {
		s += "💰 Balance: (loading...)\n\n"
	}

	// Show table information if we have game update data
	if len(r.ui.players) > 0 {
		s += "📊 Table Status:\n"
		s += fmt.Sprintf("👥 Players: %d/%d (required to start)\n", r.ui.playersJoined, r.ui.playersRequired)
		s += fmt.Sprintf("🎯 Game Phase: %s\n", r.ui.gamePhase.String())
		if r.ui.pot > 0 {
			s += fmt.Sprintf("💰 Pot: %d\n", r.ui.pot)
		}
		s += "\n"

		s += "👤 Players at table:\n"
		for _, player := range r.ui.players {
			readyStatus := ""
			if player.IsReady {
				readyStatus = " ✅ Ready"
			} else {
				readyStatus = " ⏳ Not Ready"
			}

			currentPlayerIndicator := ""
			if player.Id == r.ui.clientID {
				currentPlayerIndicator = " (You)"
			}

			s += fmt.Sprintf("  %s: 💰 %d%s%s\n",
				player.Id, player.Balance, readyStatus, currentPlayerIndicator)
		}
		s += "\n"
	} else {
		s += "Loading table information...\n\n"
	}

	for i, option := range r.ui.menuOptions {
		if i == r.ui.selectedItem {
			s += FocusedStyle.Render(fmt.Sprintf("▶ %s", option)) + "\n"
		} else {
			s += BlurredStyle.Render(fmt.Sprintf("  %s", option)) + "\n"
		}
	}

	return s
}

// RenderBetInput renders the bet input screen
func (r *Renderer) RenderBetInput() string {
	var s string
	s += TitleStyle.Render("💸 Enter Bet Amount 💸") + "\n\n"
	s += fmt.Sprintf("Current bet to call: %d\n", r.ui.currentBet)

	minBet := r.ui.currentBet + 10
	if minBet < 10 {
		minBet = 10
	}
	s += fmt.Sprintf("Minimum bet: %d\n\n", minBet)
	s += FocusedStyle.Render(fmt.Sprintf("Bet Amount: %s", r.ui.betAmount)) + "\n\n"
	s += HelpStyle.Render("Type amount and press Enter to bet, or 'q' to cancel")
	return s
}

// RenderActiveGame creates an enhanced poker table visualization
func (r *Renderer) RenderActiveGame() string {
	var s string

	// Game title
	s += TitleStyle.Render(fmt.Sprintf("🃏 Active Game - Table %s 🃏", r.ui.pc.GetCurrentTableID())) + "\n\n"

	// COMMUNITY CARDS - Most prominent section
	s += r.renderCommunityCardsSection() + "\n"

	// YOUR CARDS - Second most important
	s += r.renderYourCardsSection() + "\n"

	// Game info - balance, pot, phase
	s += r.renderGameInfo() + "\n"

	// Game status and turn indicator
	s += r.renderGameStatusHeader() + "\n"

	// Compact player information
	s += r.renderPlayersCompact() + "\n"

	// Action buttons for current player or waiting/leave options
	actionButtons := r.renderActionButtons()
	if actionButtons != "" {
		s += actionButtons + "\n"
	} else {
		// Show appropriate options when not player's turn
		if r.ui.gamePhase == pokerrpc.GamePhase_SHOWDOWN {
			s += HelpStyle.Render("🏆 Showdown - Revealing hands...") + "\n"
		} else if r.ui.currentPlayerID == "" {
			s += HelpStyle.Render("⏳ Waiting for game to start...") + "\n"
		} else if !isPlayerTurn(r.ui.currentPlayerID, r.ui.clientID) {
			s += r.renderWaitingMessage() + "\n"
		}

		// Always show leave table option when not actively playing
		s += BlurredStyle.Render("  🚪 Leave Table") + "\n"
	}

	s += "\n" + HelpStyle.Render("Use arrow keys to navigate, Enter to select, 'q' to go back")
	return s
}

// renderCommunityCardsSection creates a clear, prominent display of community cards
func (r *Renderer) renderCommunityCardsSection() string {
	var s string

	// Prominent header
	s += lipgloss.NewStyle().
		Foreground(lipgloss.Color("46")).
		Bold(true).
		Background(lipgloss.Color("22")).
		Padding(0, 2).
		Margin(0, 0, 1, 0).
		Render("🃏 COMMUNITY CARDS") + "\n"

	// Cards display
	var cardElements []string

	switch r.ui.gamePhase {
	case pokerrpc.GamePhase_WAITING, pokerrpc.GamePhase_PRE_FLOP:
		// Show placeholders
		for i := 0; i < 5; i++ {
			cardElements = append(cardElements, CardStyle.Render("🂠"))
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
			cardElements = append(cardElements, CardStyle.Render("🂠"))
		}
	}

	// Display cards centered
	cardsDisplay := strings.Join(cardElements, " ")
	s += lipgloss.NewStyle().
		Align(lipgloss.Center).
		Margin(0, 0, 1, 0).
		Render(cardsDisplay) + "\n"

	// Phase indicator
	var phaseText string
	switch r.ui.gamePhase {
	case pokerrpc.GamePhase_WAITING:
		phaseText = "⏳ Waiting for game to start"
	case pokerrpc.GamePhase_PRE_FLOP:
		phaseText = "🎯 PRE-FLOP"
	case pokerrpc.GamePhase_FLOP:
		phaseText = "🔥 FLOP"
	case pokerrpc.GamePhase_TURN:
		phaseText = "🎲 TURN"
	case pokerrpc.GamePhase_RIVER:
		phaseText = "🌊 RIVER"
	case pokerrpc.GamePhase_SHOWDOWN:
		phaseText = "🏆 SHOWDOWN"
	default:
		phaseText = fmt.Sprintf("🎯 %s", r.ui.gamePhase.String())
	}

	if isPlayerTurn(r.ui.currentPlayerID, r.ui.clientID) {
		phaseText += " ← YOUR TURN"
	}

	s += lipgloss.NewStyle().
		Foreground(lipgloss.Color("214")).
		Bold(true).
		Align(lipgloss.Center).
		Render(phaseText)

	return s
}

// renderYourCardsSection creates a clear display of the player's cards
func (r *Renderer) renderYourCardsSection() string {
	var s string

	s += lipgloss.NewStyle().
		Foreground(lipgloss.Color("39")).
		Bold(true).
		Background(lipgloss.Color("17")).
		Padding(0, 2).
		Margin(1, 0, 0, 0).
		Render("🂠 YOUR CARDS") + "\n"

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
			cardElements = []string{CardStyle.Render("🂠"), CardStyle.Render("🂠")}
		}
	} else {
		cardElements = []string{CardStyle.Render("🂠"), CardStyle.Render("🂠")}
	}

	cardsDisplay := strings.Join(cardElements, " ")
	s += lipgloss.NewStyle().
		Align(lipgloss.Center).
		Margin(0, 0, 1, 0).
		Render(cardsDisplay)

	return s
}

// renderGameInfo displays balance, pot, and game phase information
func (r *Renderer) renderGameInfo() string {
	balanceText := "💰 Balance: (loading...)"
	if balance, err := r.ui.pc.GetBalance(r.ui.ctx); err == nil {
		balanceText = fmt.Sprintf("💰 Balance: %d", balance)
	}

	potDisplay := fmt.Sprintf("💰 POT: %d", r.ui.pot)
	if r.ui.currentBet > 0 {
		potDisplay += fmt.Sprintf(" | Current Bet: %d", r.ui.currentBet)
	}

	gameInfo := balanceText + " | " + potDisplay
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("140")).
		Margin(1, 0).
		Render(gameInfo)
}

// renderGameStatusHeader shows whose turn it is and game status
func (r *Renderer) renderGameStatusHeader() string {
	var statusMsg string

	// Determine game status
	if r.ui.currentPlayerID == "" {
		statusMsg = "⏳ Waiting for game to start..."
	} else if isPlayerTurn(r.ui.currentPlayerID, r.ui.clientID) {
		statusMsg = "🎯 YOUR TURN - Choose your action below"
	} else {
		// Find the current player's name for display
		currentPlayerName := r.ui.currentPlayerID
		if len(currentPlayerName) > 12 {
			currentPlayerName = currentPlayerName[:12] + "..."
		}
		statusMsg = fmt.Sprintf("⏰ Waiting for %s to act...", currentPlayerName)
	}

	return TitleStyle.Render(statusMsg)
}

// renderWaitingMessage shows appropriate waiting message
func (r *Renderer) renderWaitingMessage() string {
	var waitingMsg string

	if r.ui.currentPlayerID == "" {
		waitingMsg = "⏳ Game is starting..."
	} else {
		// Find the current player's name for display
		currentPlayerName := r.ui.currentPlayerID
		if len(currentPlayerName) > 12 {
			currentPlayerName = currentPlayerName[:12] + "..."
		}
		waitingMsg = fmt.Sprintf("⏰ Waiting for %s to make their move...", currentPlayerName)
	}

	return HelpStyle.Render(waitingMsg)
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

// formatPlayerInfo creates a formatted string for player information
func (r *Renderer) formatPlayerInfo(player *pokerrpc.Player) string {
	var info []string

	// Player ID (truncated for display)
	playerName := player.Id
	if len(playerName) > 12 {
		playerName = playerName[:12] + "..."
	}
	info = append(info, fmt.Sprintf("👤 %s", playerName))

	// Balance
	info = append(info, fmt.Sprintf("💰 Balance: %d", player.Balance))

	// Current bet
	if player.CurrentBet > 0 {
		info = append(info, fmt.Sprintf("🎯 Bet: %d", player.CurrentBet))
	}

	// Status indicators - improved logic
	var status []string

	// Check if player is you
	if player.Id == r.ui.clientID {
		status = append(status, "🔵 You")
	}

	// Check if it's their turn (and they haven't folded)
	if player.Id == r.ui.currentPlayerID && !player.Folded {
		status = append(status, "⏰ Turn")
	}

	// Only show folded if player has actually folded
	if player.Folded {
		status = append(status, "❌ Folded")
	} else {
		// Player is active in the game
		if r.ui.gamePhase != pokerrpc.GamePhase_WAITING {
			status = append(status, "✅ Active")
		} else {
			// In waiting phase, show ready status
			if player.IsReady {
				status = append(status, "✅ Ready")
			} else {
				status = append(status, "⏳ Not Ready")
			}
		}
	}

	if len(status) > 0 {
		info = append(info, strings.Join(status, " "))
	}

	return strings.Join(info, "\n")
}

// renderActionButtons creates interactive action buttons for the current player
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
	result += TitleStyle.Render("🎯 YOUR TURN - Choose your action 🎯") + "\n\n"

	// Use the actual menuOptions from the UI state instead of hardcoded actions
	for i, option := range r.ui.menuOptions {
		var icon string
		var desc string

		switch option {
		case optionCheck:
			icon = "✅"
			desc = "Check"
		case optionBet:
			icon = "💸"
			desc = "Bet/Raise"
		case optionFold:
			icon = "❌"
			desc = "Fold"
		case optionLeaveTable:
			icon = "🚪"
			desc = "Leave Table"
		default:
			icon = "🔧"
			desc = string(option)
		}

		buttonText := fmt.Sprintf("%s %s", icon, desc)

		if i == r.ui.selectedItem {
			result += FocusedStyle.Render(fmt.Sprintf("▶ %s", buttonText)) + "\n"
		} else {
			result += BlurredStyle.Render(fmt.Sprintf("  %s", buttonText)) + "\n"
		}
	}

	return result
}

// formatCard creates a visual representation of a playing card
func (r *Renderer) formatCard(card *pokerrpc.Card) string {
	if card == nil {
		return "🂠"
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

// renderPlayersCompact creates a compact single-line representation of players for when action buttons are shown
func (r *Renderer) renderPlayersCompact() string {
	if len(r.ui.players) == 0 {
		return HelpStyle.Render("👥 No players at table")
	}

	var result strings.Builder
	result.WriteString("👥 Players: ")

	var playerInfos []string
	for i, player := range r.ui.players {
		// Player name (truncated)
		playerName := player.Id
		if len(playerName) > 10 {
			playerName = playerName[:10] + "..."
		}

		// Create status indicators with colors
		var statusColor lipgloss.Color
		var statusIcon string

		if player.Id == r.ui.clientID {
			statusColor = lipgloss.Color("39") // Blue for you
			statusIcon = "🔵"
		} else if player.Id == r.ui.currentPlayerID && !player.Folded {
			statusColor = lipgloss.Color("46") // Green for current turn
			statusIcon = "⏰"
		} else if player.Folded {
			statusColor = lipgloss.Color("241") // Gray for folded
			statusIcon = "❌"
		} else {
			statusColor = lipgloss.Color("255") // White for active
			statusIcon = "✅"
		}

		// Format player info with styling
		playerInfo := fmt.Sprintf("%s S%d:%s(💰%d)", statusIcon, i+1, playerName, player.Balance)

		// Add current bet if they have one
		if player.CurrentBet > 0 {
			playerInfo += fmt.Sprintf("(🎯%d)", player.CurrentBet)
		}

		styledPlayerInfo := lipgloss.NewStyle().
			Foreground(statusColor).
			Render(playerInfo)

		playerInfos = append(playerInfos, styledPlayerInfo)
	}

	result.WriteString(strings.Join(playerInfos, " "))

	// Add a subtle border and margin to separate from other elements
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("240")).
		Padding(0, 1).
		Margin(1, 0).
		Render(result.String())
}
