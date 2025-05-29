package ui

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/vctt94/poker-bisonrelay/pkg/client"
	"github.com/vctt94/poker-bisonrelay/pkg/rpc/grpc/pokerrpc"
)

var (
	focusedStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
	blurredStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	titleStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true).MarginLeft(2)
	gameInfoStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("140")).MarginTop(1)
	helpStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Margin(1, 0)
)

type menuOption string

const (
	optionListTables   menuOption = "List Tables"
	optionCreateTable  menuOption = "Create Table"
	optionJoinTable    menuOption = "Join Table"
	optionLeaveTable   menuOption = "Leave Table"
	optionCheckBalance menuOption = "Check Balance"
	optionSetReady     menuOption = "Set Ready"
	optionSetUnready   menuOption = "Set Unready"
	optionQuit         menuOption = "Quit"
	// Poker actions
	optionCheck menuOption = "Check"
	optionBet   menuOption = "Bet"
	optionFold  menuOption = "Fold"
)

// screenState represents the current screen in the UI
type screenState int

const (
	stateMainMenu screenState = iota
	stateTableList
	stateCreateTable
	stateJoinTable
	stateGameLobby
	stateActiveGame // New state for when the game is actively running
)

// PokerUI contains all the state for our poker client UI
type PokerUI struct {
	// Common fields
	ctx          context.Context
	clientID     string
	pc           *client.PokerClient
	state        screenState
	err          error
	selectedItem int
	tables       []*pokerrpc.Table
	menuOptions  []menuOption

	// Temporary message
	message string

	// Selected table in table list
	selectedTable int

	// Create table form inputs (just strings for simplicity)
	smallBlind        string
	bigBlind          string
	requiredPlayers   string
	buyIn             string
	minBalance        string
	selectedFormField int // Track which form field is selected

	// For join table
	tableIdInput string

	// Game state tracking
	gamePhase       pokerrpc.GamePhase
	pot             int64
	currentBet      int64
	players         []*pokerrpc.Player
	communityCards  []*pokerrpc.Card
	currentPlayerID string
	playersRequired int32
	playersJoined   int32

	// Command dispatcher for backend operations
	dispatcher *CommandDispatcher
}

// NewPokerUI creates a new poker UI model
func NewPokerUI(ctx context.Context, client *client.PokerClient) PokerUI {
	// Initial menu options
	menuOptions := []menuOption{
		optionListTables,
		optionCreateTable,
		optionJoinTable,
		optionCheckBalance,
		optionQuit,
	}

	ui := PokerUI{
		ctx:      ctx,
		clientID: client.ID,
		pc:       client,

		state:           stateMainMenu,
		menuOptions:     menuOptions,
		smallBlind:      "10",
		bigBlind:        "20",
		requiredPlayers: "2",
		buyIn:           "100",
		minBalance:      "100",
	}

	// Create dispatcher after UI is initialized
	ui.dispatcher = NewCommandDispatcher(ctx, client.ID, client)

	return ui
}

func (m PokerUI) Init() tea.Cmd {
	return m.dispatcher.getBalanceCmd()
}

func (m PokerUI) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			if m.state == stateMainMenu {
				return m, tea.Quit
			} else {
				// For table-related screens, just go back to main menu without leaving the table
				if m.state == stateGameLobby || m.state == stateActiveGame {
					m.state = stateMainMenu
					m.message = ""
					m.err = nil
					m.updateMenuOptionsForGameState()
					return m, nil
				} else {
					// For other screens (table list, create table, join table), go back to main menu
					m.resetToMainMenu()
					return m, nil
				}
			}
		case "up", "k":
			if m.state == stateMainMenu || m.state == stateGameLobby {
				m.selectedItem = max(0, m.selectedItem-1)
			} else if m.state == stateTableList && len(m.tables) > 0 {
				m.selectedTable = max(0, m.selectedTable-1)
			} else if m.state == stateCreateTable {
				m.selectedFormField = max(0, m.selectedFormField-1)
			}
		case "down", "j":
			if m.state == stateMainMenu || m.state == stateGameLobby {
				m.selectedItem = min(len(m.menuOptions)-1, m.selectedItem+1)
			} else if m.state == stateTableList && len(m.tables) > 0 {
				m.selectedTable = min(len(m.tables)-1, m.selectedTable+1)
			} else if m.state == stateCreateTable {
				m.selectedFormField = min(4, m.selectedFormField+1)
			}
		case "enter":
			switch m.state {
			case stateMainMenu:
				switch m.menuOptions[m.selectedItem] {
				case "Return to Table":
					tableID := m.pc.GetCurrentTableID()
					if tableID != "" {
						// Determine which state to return to based on game phase
						if m.gamePhase == pokerrpc.GamePhase_WAITING {
							m.state = stateGameLobby
						} else {
							m.state = stateActiveGame
						}
						m.updateMenuOptionsForGameState()
						// Refresh game state
						cmds = append(cmds, m.dispatcher.checkGameStateCmd())
					}
				case optionListTables:
					m.state = stateTableList
					cmds = append(cmds, m.dispatcher.getTablesCmd())
				case optionCreateTable:
					m.state = stateCreateTable
				case optionJoinTable:
					m.state = stateJoinTable
				case optionCheckBalance:
					cmds = append(cmds, m.dispatcher.getBalanceCmd())
				case optionQuit:
					return m, tea.Quit
				}
			case stateGameLobby:
				// Handle game lobby menu selections
				switch m.menuOptions[m.selectedItem] {
				case optionLeaveTable:
					cmds = append(cmds, m.dispatcher.leaveTableCmd())
				case optionSetReady:
					cmds = append(cmds, m.dispatcher.setPlayerReadyCmd())
				case optionSetUnready:
					cmds = append(cmds, m.dispatcher.setPlayerUnreadyCmd())
				case optionCheckBalance:
					cmds = append(cmds, m.dispatcher.getBalanceCmd())
				case optionQuit:
					return m, tea.Quit
				}
			case stateActiveGame:
				// Handle active game menu selections
				switch m.menuOptions[m.selectedItem] {
				case optionCheck:
					cmds = append(cmds, m.dispatcher.checkCmd())
				case optionFold:
					cmds = append(cmds, m.dispatcher.foldCmd())
				case optionBet:
					// For simplicity, just bet the current bet + 10
					betAmount := m.currentBet + 10
					cmds = append(cmds, m.dispatcher.betCmd(betAmount))
				case optionLeaveTable:
					cmds = append(cmds, m.dispatcher.leaveTableCmd())
				}
			case stateTableList:
				if len(m.tables) > 0 {
					// Join the selected table
					selectedTable := m.tables[m.selectedTable]
					cmds = append(cmds, m.dispatcher.joinTableCmd(selectedTable.Id))
				}
			case stateCreateTable:
				// Parse form inputs and create table
				smallBlind, _ := strconv.ParseInt(m.smallBlind, 10, 64)
				bigBlind, _ := strconv.ParseInt(m.bigBlind, 10, 64)
				requiredPlayers, _ := strconv.ParseInt(m.requiredPlayers, 10, 32)
				buyIn, _ := strconv.ParseInt(m.buyIn, 10, 64)
				minBalance, _ := strconv.ParseInt(m.minBalance, 10, 64)

				config := client.TableCreateConfig{
					SmallBlind: smallBlind,
					BigBlind:   bigBlind,
					MinPlayers: int32(requiredPlayers),
					MaxPlayers: int32(requiredPlayers),
					BuyIn:      buyIn,
					MinBalance: minBalance,
				}
				cmds = append(cmds, m.dispatcher.createTableCmd(config))
			case stateJoinTable:
				if m.tableIdInput != "" {
					cmds = append(cmds, m.dispatcher.joinTableCmd(m.tableIdInput))
				}
			}
		}

		// Handle typing in forms
		if m.state == stateCreateTable {
			switch m.selectedFormField {
			case 0: // small blind
				if msg.String() == "backspace" && len(m.smallBlind) > 0 {
					m.smallBlind = m.smallBlind[:len(m.smallBlind)-1]
				} else if len(msg.String()) == 1 && msg.String() >= "0" && msg.String() <= "9" {
					m.smallBlind += msg.String()
				}
			case 1: // big blind
				if msg.String() == "backspace" && len(m.bigBlind) > 0 {
					m.bigBlind = m.bigBlind[:len(m.bigBlind)-1]
				} else if len(msg.String()) == 1 && msg.String() >= "0" && msg.String() <= "9" {
					m.bigBlind += msg.String()
				}
			case 2: // required players
				if msg.String() == "backspace" && len(m.requiredPlayers) > 0 {
					m.requiredPlayers = m.requiredPlayers[:len(m.requiredPlayers)-1]
				} else if len(msg.String()) == 1 && msg.String() >= "0" && msg.String() <= "9" {
					m.requiredPlayers += msg.String()
				}
			case 3: // buy in
				if msg.String() == "backspace" && len(m.buyIn) > 0 {
					m.buyIn = m.buyIn[:len(m.buyIn)-1]
				} else if len(msg.String()) == 1 && msg.String() >= "0" && msg.String() <= "9" {
					m.buyIn += msg.String()
				}
			case 4: // min balance
				if msg.String() == "backspace" && len(m.minBalance) > 0 {
					m.minBalance = m.minBalance[:len(m.minBalance)-1]
				} else if len(msg.String()) == 1 && msg.String() >= "0" && msg.String() <= "9" {
					m.minBalance += msg.String()
				}
			}
		} else if m.state == stateJoinTable {
			if msg.String() == "backspace" && len(m.tableIdInput) > 0 {
				m.tableIdInput = m.tableIdInput[:len(m.tableIdInput)-1]
			} else if len(msg.String()) == 1 {
				m.tableIdInput += msg.String()
			}
		}

	case notificationMsg:
		notification := (*pokerrpc.Notification)(msg)
		cmd := m.handleNotification(notification)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}

	case errorMsg:
		m.err = error(msg)
		m.message = fmt.Sprintf("Error: %v", m.err)

		// If we get an error while checking game state and we're at a table,
		// it likely means the table no longer exists (e.g., host left)
		// Reset to main menu in this case
		tableID := m.pc.GetCurrentTableID()
		if tableID != "" && (m.state == stateGameLobby || m.state == stateActiveGame) {
			// Check if the error indicates the table doesn't exist
			errorStr := m.err.Error()
			if strings.Contains(errorStr, "table not found") ||
				strings.Contains(errorStr, "Table not found") ||
				strings.Contains(errorStr, "not found") {
				m.resetToMainMenu()
				m.message = "Table no longer exists (host may have left)"
				m.err = nil
			}
		}

	case tablesMsg:
		m.tables = []*pokerrpc.Table(msg)
		m.selectedTable = 0

	case gameUpdateMsg:
		gameUpdate := (*pokerrpc.GameUpdate)(msg)
		if gameUpdate != nil {
			m.gamePhase = gameUpdate.Phase
			m.pot = gameUpdate.Pot
			m.currentBet = gameUpdate.CurrentBet
			m.players = gameUpdate.Players
			m.communityCards = gameUpdate.CommunityCards
			m.currentPlayerID = gameUpdate.CurrentPlayer
			m.playersRequired = gameUpdate.PlayersRequired
			m.playersJoined = gameUpdate.PlayersJoined

			// Update state based on game phase only if we're not in main menu
			if m.state != stateMainMenu {
				if m.gamePhase == pokerrpc.GamePhase_WAITING {
					m.state = stateGameLobby
				} else {
					m.state = stateActiveGame
				}
				m.updateMenuOptionsForGameState()
			}
		}

	case tickMsg:
		tableID := m.pc.GetCurrentTableID()
		if tableID != "" {
			cmds = append(cmds, m.dispatcher.checkGameStateCmd())
		}
		cmds = append(cmds, gameUpdateTicker())
	}

	return m, tea.Batch(cmds...)
}

// handleNotification processes notifications from the backend
func (m *PokerUI) handleNotification(notification *pokerrpc.Notification) tea.Cmd {
	switch notification.Type {
	case pokerrpc.NotificationType_BALANCE_UPDATED:
		m.message = fmt.Sprintf("Balance: %d", notification.NewBalance)
		return m.dispatcher.getBalanceCmd()

	case pokerrpc.NotificationType_PLAYER_JOINED:
		if notification.PlayerId == m.clientID {
			m.state = stateGameLobby
			m.message = fmt.Sprintf("Joined table %s", notification.TableId)
			m.updateMenuOptionsForGameState()
			// Start game state polling and ticker
			return tea.Batch(
				m.dispatcher.checkGameStateCmd(),
				gameUpdateTicker(),
			)
		}

	case pokerrpc.NotificationType_PLAYER_LEFT:
		if notification.PlayerId == m.clientID {
			m.resetToMainMenu()
			m.message = "Left table"
			return nil
		}

	case pokerrpc.NotificationType_TABLE_CREATED:
		if notification.PlayerId == m.clientID {
			m.state = stateGameLobby
			m.message = fmt.Sprintf("Created table %s", notification.TableId)
			m.updateMenuOptionsForGameState()
			// Start game state polling and ticker
			return tea.Batch(
				m.dispatcher.checkGameStateCmd(),
				gameUpdateTicker(),
			)
		}

	case pokerrpc.NotificationType_TABLE_REMOVED:
		currentTableID := m.pc.GetCurrentTableID()
		if notification.TableId == currentTableID {
			m.resetToMainMenu()
			m.message = "Table was closed"
			return nil
		}

	case pokerrpc.NotificationType_PLAYER_READY:
		if notification.PlayerId == m.clientID {
			m.message = "You are now ready"
			return nil
		}

	case pokerrpc.NotificationType_PLAYER_UNREADY:
		if notification.PlayerId == m.clientID {
			m.message = "You are now unready"
			return nil
		}

	case pokerrpc.NotificationType_ALL_PLAYERS_READY:
		m.message = "All players are ready! Game starting soon..."
		return nil

	case pokerrpc.NotificationType_GAME_STARTED:
		m.state = stateActiveGame
		m.message = "Game started!"
		m.updateMenuOptionsForGameState()
		return m.dispatcher.getBalanceCmd()

	case pokerrpc.NotificationType_GAME_ENDED:
		m.state = stateGameLobby
		m.message = "Game ended"
		m.updateMenuOptionsForGameState()
		return m.dispatcher.getBalanceCmd()

	case pokerrpc.NotificationType_BET_MADE:
		if notification.PlayerId == m.clientID {
			m.message = fmt.Sprintf("Bet placed: %d", notification.Amount)
			return nil
		}

	case pokerrpc.NotificationType_PLAYER_FOLDED:
		if notification.PlayerId == m.clientID {
			m.message = "You folded"
			return nil
		}

	case pokerrpc.NotificationType_NEW_ROUND:
		if notification.PlayerId == m.clientID {
			m.message = "Action completed"
			return nil
		}

	case pokerrpc.NotificationType_SHOWDOWN_RESULT:
		m.message = "Showdown complete"
		return nil

	case pokerrpc.NotificationType_TIP_RECEIVED:
		m.message = fmt.Sprintf("Tip received: %d", notification.Amount)
		return nil

	default:
		m.message = notification.Message
		return nil
	}
	return nil
}

// View renders the current state of the UI
func (m PokerUI) View() string {
	var s string

	// Show any temporary message
	if m.message != "" {
		s += titleStyle.Render(m.message) + "\n\n"
	}

	// Show error if any
	if m.err != nil {
		s += lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render(fmt.Sprintf("Error: %v", m.err)) + "\n\n"
	}

	switch m.state {
	case stateMainMenu:
		s += titleStyle.Render("Poker Client - Main Menu") + "\n\n"
		s += fmt.Sprintf("Client ID: %s\n", m.clientID)

		// Get current balance from poker client
		if balance, err := m.pc.GetBalance(m.ctx); err == nil {
			s += fmt.Sprintf("Balance: %d\n", balance)
		} else {
			s += "Balance: (loading...)\n"
		}

		// Show current table info if player is at a table
		currentTableID := m.pc.GetCurrentTableID()
		if currentTableID != "" {
			s += fmt.Sprintf("Current Table: %s (Phase: %s)\n", currentTableID, m.gamePhase.String())
		}
		s += "\n"

		for i, option := range m.menuOptions {
			if i == m.selectedItem {
				s += focusedStyle.Render(fmt.Sprintf("> %s", option)) + "\n"
			} else {
				s += blurredStyle.Render(fmt.Sprintf("  %s", option)) + "\n"
			}
		}

	case stateTableList:
		s += titleStyle.Render("Available Tables") + "\n\n"
		if len(m.tables) == 0 {
			s += "No tables available.\n"
		} else {
			for i, table := range m.tables {
				style := blurredStyle
				if i == m.selectedTable {
					style = focusedStyle
				}
				s += style.Render(fmt.Sprintf("%s - ID: %s, Players: %d/%d, Stakes: %d/%d",
					func() string {
						if i == m.selectedTable {
							return ">"
						}
						return " "
					}(),
					table.Id,
					table.CurrentPlayers,
					table.MaxPlayers,
					table.SmallBlind,
					table.BigBlind,
				)) + "\n"
			}
		}
		s += "\n" + helpStyle.Render("Press Enter to join selected table, or 'q' to go back")

	case stateCreateTable:
		s += titleStyle.Render("Create New Table") + "\n\n"

		fields := []struct {
			label string
			value string
		}{
			{"Small Blind", m.smallBlind},
			{"Big Blind", m.bigBlind},
			{"Required Players", m.requiredPlayers},
			{"Buy In", m.buyIn},
			{"Min Balance", m.minBalance},
		}

		for i, field := range fields {
			style := blurredStyle
			if i == m.selectedFormField {
				style = focusedStyle
			}
			s += style.Render(fmt.Sprintf("%s %s: %s",
				func() string {
					if i == m.selectedFormField {
						return ">"
					}
					return " "
				}(),
				field.label,
				field.value,
			)) + "\n"
		}
		s += "\n" + helpStyle.Render("Use arrow keys to navigate, type to edit, Enter to create table")

	case stateJoinTable:
		s += titleStyle.Render("Join Table") + "\n\n"
		s += focusedStyle.Render(fmt.Sprintf("Table ID: %s", m.tableIdInput)) + "\n\n"
		s += helpStyle.Render("Enter table ID and press Enter to join")

	case stateGameLobby:
		s += titleStyle.Render(fmt.Sprintf("Game Lobby - Table %s", m.pc.GetCurrentTableID())) + "\n\n"

		// Get current balance from poker client
		if balance, err := m.pc.GetBalance(m.ctx); err == nil {
			s += fmt.Sprintf("Balance: %d\n\n", balance)
		} else {
			s += "Balance: (loading...)\n\n"
		}

		// Show table information if we have game update data
		if len(m.players) > 0 {
			s += "Table Status:\n"
			s += fmt.Sprintf("Players: %d/%d (required to start)\n", m.playersJoined, m.playersRequired)
			s += fmt.Sprintf("Game Phase: %s\n", m.gamePhase.String())
			if m.pot > 0 {
				s += fmt.Sprintf("Pot: %d\n", m.pot)
			}
			s += "\n"

			s += "Players at table:\n"
			for _, player := range m.players {
				readyStatus := ""
				if player.IsReady {
					readyStatus = " ✓ Ready"
				} else {
					readyStatus = " ⏳ Not Ready"
				}

				currentPlayerIndicator := ""
				if player.Id == m.clientID {
					currentPlayerIndicator = " (You)"
				}

				s += fmt.Sprintf("  %s: Balance %d%s%s\n",
					player.Id, player.Balance, readyStatus, currentPlayerIndicator)
			}
			s += "\n"
		} else {
			s += "Loading table information...\n\n"
		}

		for i, option := range m.menuOptions {
			if i == m.selectedItem {
				s += focusedStyle.Render(fmt.Sprintf("> %s", option)) + "\n"
			} else {
				s += blurredStyle.Render(fmt.Sprintf("  %s", option)) + "\n"
			}
		}

	case stateActiveGame:
		s += titleStyle.Render(fmt.Sprintf("Active Game - Table %s", m.pc.GetCurrentTableID())) + "\n\n"

		// Game info
		s += gameInfoStyle.Render(fmt.Sprintf("Pot: %d | Current Bet: %d | Phase: %s",
			m.pot, m.currentBet, m.gamePhase.String())) + "\n\n"

		// Community cards
		if len(m.communityCards) > 0 {
			s += "Community Cards: "
			for _, card := range m.communityCards {
				s += fmt.Sprintf("[%s%s] ", card.Value, card.Suit)
			}
			s += "\n\n"
		}

		// Players
		if len(m.players) > 0 {
			s += "Players:\n"
			for _, player := range m.players {
				playerInfo := fmt.Sprintf("  %s: Balance %d, Bet %d",
					player.Id, player.Balance, player.CurrentBet)
				if player.Folded {
					playerInfo += " (Folded)"
				}
				if player.Id == m.currentPlayerID {
					playerInfo += " <- Current Turn"
				}
				if player.Id == m.clientID {
					playerInfo = focusedStyle.Render(playerInfo + " (You)")
				} else {
					playerInfo = blurredStyle.Render(playerInfo)
				}
				s += playerInfo + "\n"
			}
			s += "\n"
		}

		// Menu options for current player
		if isPlayerTurn(m.currentPlayerID, m.clientID) {
			m.menuOptions = []menuOption{
				optionCheck,
				optionBet,
				optionFold,
				optionLeaveTable,
			}
		} else {
			m.menuOptions = []menuOption{
				optionLeaveTable,
			}
		}
	}

	s += "\n" + helpStyle.Render("Press 'q' to go back/quit, Ctrl+C to force quit")
	return s
}

// Run starts the UI
func Run(ctx context.Context, client *client.PokerClient) {
	model := NewPokerUI(ctx, client)

	p := tea.NewProgram(model, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		log.Fatalf("Error running UI: %v", err)
	}
}

// resetToMainMenu resets the model to the main menu state, clearing all table and game data
func (m *PokerUI) resetToMainMenu() {
	m.state = stateMainMenu
	m.message = ""
	m.err = nil
	m.resetGameState()
	m.updateMenuOptionsForGameState()
}

// resetGameState resets all game-related fields while keeping table connection
func (m *PokerUI) resetGameState() {
	m.players = nil
	m.communityCards = nil
	m.gamePhase = pokerrpc.GamePhase_WAITING
	m.pot = 0
	m.currentBet = 0
	m.currentPlayerID = ""
	m.playersRequired = 0
	m.playersJoined = 0
}

// updateMenuOptionsForGameState updates the menu options based on the current game state
func (m *PokerUI) updateMenuOptionsForGameState() {
	if m.state == stateMainMenu {
		m.menuOptions = []menuOption{
			optionListTables,
			optionCreateTable,
			optionJoinTable,
			optionCheckBalance,
			optionQuit,
		}
		currentTableID := m.pc.GetCurrentTableID()
		if currentTableID != "" {
			m.menuOptions = append([]menuOption{"Return to Table"}, m.menuOptions...)
		}
	} else if m.state == stateGameLobby {
		m.menuOptions = []menuOption{
			optionSetReady,
			optionSetUnready,
			optionLeaveTable,
			optionCheckBalance,
			optionQuit,
		}
	} else if m.state == stateActiveGame {
		if isPlayerTurn(m.currentPlayerID, m.clientID) {
			m.menuOptions = []menuOption{
				optionCheck,
				optionBet,
				optionFold,
				optionLeaveTable,
			}
		} else {
			m.menuOptions = []menuOption{
				optionLeaveTable,
			}
		}
	}
}
