package ui

import (
	"context"
	"fmt"
	"log"
	"strconv"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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

// Model contains all the state for our UI
type Model struct {
	// Common fields
	ctx          context.Context
	clientID     string
	lobbyClient  pokerrpc.LobbyServiceClient
	pokerClient  pokerrpc.PokerServiceClient
	state        screenState
	err          error
	balance      int64
	selectedItem int
	tableID      string
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
}

// NewModel creates a new UI model
func NewModel(ctx context.Context, clientID string, lobbyClient pokerrpc.LobbyServiceClient, pokerClient pokerrpc.PokerServiceClient) Model {
	// Initial menu options
	menuOptions := []menuOption{
		optionListTables,
		optionCreateTable,
		optionJoinTable,
		optionCheckBalance,
		optionQuit,
	}

	return Model{
		ctx:             ctx,
		clientID:        clientID,
		lobbyClient:     lobbyClient,
		pokerClient:     pokerClient,
		state:           stateMainMenu,
		menuOptions:     menuOptions,
		smallBlind:      "10",
		bigBlind:        "20",
		requiredPlayers: "2",
		buyIn:           "100",
		minBalance:      "100",
	}
}

func (m Model) Init() tea.Cmd {
	return getBalanceCmd(m.ctx, m.clientID, m.lobbyClient)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			if m.state == stateMainMenu {
				return m, tea.Quit
			} else {
				// Return to main menu for any other screen
				m.state = stateMainMenu
				m.tableID = ""
				m.message = ""
				m.err = nil
				// Reset menu options to main menu
				m.menuOptions = []menuOption{
					optionListTables,
					optionCreateTable,
					optionJoinTable,
					optionCheckBalance,
					optionQuit,
				}
				return m, nil
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
				case optionListTables:
					m.state = stateTableList
					cmds = append(cmds, getTablesCmd(m.ctx, m.lobbyClient))
				case optionCreateTable:
					m.state = stateCreateTable
				case optionJoinTable:
					m.state = stateJoinTable
				case optionCheckBalance:
					cmds = append(cmds, getBalanceCmd(m.ctx, m.clientID, m.lobbyClient))
				case optionLeaveTable:
					cmds = append(cmds, leaveTableCmd(m.ctx, m.clientID, m.tableID, m.lobbyClient))
				case optionSetReady:
					cmds = append(cmds, setPlayerReadyCmd(m.ctx, m.clientID, m.tableID, m.lobbyClient))
				case optionSetUnready:
					cmds = append(cmds, setPlayerUnreadyCmd(m.ctx, m.clientID, m.tableID, m.lobbyClient))
				case optionQuit:
					return m, tea.Quit
				}
			case stateGameLobby:
				// Handle game lobby menu selections
				switch m.menuOptions[m.selectedItem] {
				case optionLeaveTable:
					cmds = append(cmds, leaveTableCmd(m.ctx, m.clientID, m.tableID, m.lobbyClient))
				case optionSetReady:
					cmds = append(cmds, setPlayerReadyCmd(m.ctx, m.clientID, m.tableID, m.lobbyClient))
				case optionSetUnready:
					cmds = append(cmds, setPlayerUnreadyCmd(m.ctx, m.clientID, m.tableID, m.lobbyClient))
				case optionCheckBalance:
					cmds = append(cmds, getBalanceCmd(m.ctx, m.clientID, m.lobbyClient))
				case optionQuit:
					return m, tea.Quit
				}
			case stateActiveGame:
				// Handle active game menu selections
				switch m.menuOptions[m.selectedItem] {
				case optionCheck:
					cmds = append(cmds, checkCmd(m.ctx, m.clientID, m.tableID, m.pokerClient))
				case optionFold:
					cmds = append(cmds, foldCmd(m.ctx, m.clientID, m.tableID, m.pokerClient))
				case optionBet:
					// For simplicity, just bet the current bet + 10
					betAmount := m.currentBet + 10
					cmds = append(cmds, betCmd(m.ctx, m.clientID, m.tableID, betAmount, m.pokerClient))
				case optionLeaveTable:
					cmds = append(cmds, leaveTableCmd(m.ctx, m.clientID, m.tableID, m.lobbyClient))
				}
			case stateTableList:
				if len(m.tables) > 0 {
					// Join the selected table
					selectedTable := m.tables[m.selectedTable]
					m.tableID = selectedTable.Id
					cmds = append(cmds, joinTableCmd(m.ctx, m.clientID, selectedTable.Id, m.lobbyClient))
				}
			case stateCreateTable:
				// Parse form inputs and create table
				smallBlind, _ := strconv.ParseInt(m.smallBlind, 10, 64)
				bigBlind, _ := strconv.ParseInt(m.bigBlind, 10, 64)
				requiredPlayers, _ := strconv.ParseInt(m.requiredPlayers, 10, 32)
				buyIn, _ := strconv.ParseInt(m.buyIn, 10, 64)
				minBalance, _ := strconv.ParseInt(m.minBalance, 10, 64)

				cmds = append(cmds, createTableCmd(m.ctx, m.clientID, smallBlind, bigBlind, int32(requiredPlayers), 6, buyIn, minBalance, m.lobbyClient))
			case stateJoinTable:
				if m.tableIdInput != "" {
					m.tableID = m.tableIdInput
					cmds = append(cmds, joinTableCmd(m.ctx, m.clientID, m.tableIdInput, m.lobbyClient))
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

	case balanceMsg:
		m.balance = int64(msg)
		m.message = fmt.Sprintf("Balance: %d", m.balance)

	case errorMsg:
		m.err = error(msg)
		m.message = fmt.Sprintf("Error: %v", m.err)

	case tablesMsg:
		m.tables = []*pokerrpc.Table(msg)
		m.selectedTable = 0

	case tableJoinedMsg:
		m.state = stateGameLobby
		m.message = fmt.Sprintf("Joined table %s", m.tableID)
		updateMenuOptionsForGameState(&m)
		// Start checking game state periodically
		cmds = append(cmds, gameUpdateTicker())

	case tableLeftMsg:
		m.state = stateMainMenu
		m.tableID = ""
		m.message = "Left table"
		// Reset menu options
		m.menuOptions = []menuOption{
			optionListTables,
			optionCreateTable,
			optionJoinTable,
			optionCheckBalance,
			optionQuit,
		}

	case tableCreatedMsg:
		m.tableID = string(msg)
		m.state = stateGameLobby
		m.message = fmt.Sprintf("Created table %s", m.tableID)
		updateMenuOptionsForGameState(&m)
		// Start checking game state periodically
		cmds = append(cmds, gameUpdateTicker())

	case playerReadyMsg:
		m.message = "You are now ready"

	case playerUnreadyMsg:
		m.message = "You are now unready"

	case gameStartedMsg:
		m.state = stateActiveGame
		m.message = "Game started!"
		updateMenuOptionsForGameState(&m)

	case gameUpdateMsg:
		gameUpdate := (*pokerrpc.GameUpdate)(msg)
		if gameUpdate != nil {
			m.gamePhase = gameUpdate.Phase
			m.pot = gameUpdate.Pot
			m.currentBet = gameUpdate.CurrentBet
			m.players = gameUpdate.Players
			m.communityCards = gameUpdate.CommunityCards
			m.currentPlayerID = gameUpdate.CurrentPlayer

			// Update state based on game phase
			if m.gamePhase == pokerrpc.GamePhase_WAITING {
				m.state = stateGameLobby
			} else {
				m.state = stateActiveGame
			}
			updateMenuOptionsForGameState(&m)
		}
		// Continue checking for updates
		cmds = append(cmds, checkGameStateCmd(m.ctx, m.clientID, m.tableID, m.pokerClient))

	case tickMsg:
		if m.tableID != "" {
			cmds = append(cmds, checkGameStateCmd(m.ctx, m.clientID, m.tableID, m.pokerClient))
		}
		cmds = append(cmds, gameUpdateTicker())
	}

	return m, tea.Batch(cmds...)
}

// View renders the current state of the UI
func (m Model) View() string {
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
		s += fmt.Sprintf("Balance: %d\n\n", m.balance)

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
		s += titleStyle.Render(fmt.Sprintf("Game Lobby - Table %s", m.tableID)) + "\n\n"
		s += fmt.Sprintf("Balance: %d\n\n", m.balance)

		for i, option := range m.menuOptions {
			if i == m.selectedItem {
				s += focusedStyle.Render(fmt.Sprintf("> %s", option)) + "\n"
			} else {
				s += blurredStyle.Render(fmt.Sprintf("  %s", option)) + "\n"
			}
		}

	case stateActiveGame:
		s += titleStyle.Render(fmt.Sprintf("Active Game - Table %s", m.tableID)) + "\n\n"

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
		for i, option := range m.menuOptions {
			if i == m.selectedItem {
				s += focusedStyle.Render(fmt.Sprintf("> %s", option)) + "\n"
			} else {
				s += blurredStyle.Render(fmt.Sprintf("  %s", option)) + "\n"
			}
		}
	}

	s += "\n" + helpStyle.Render("Press 'q' to quit or go back")
	return s
}

// Run starts the UI
func Run(ctx context.Context, clientID string, lobbyClient pokerrpc.LobbyServiceClient, pokerClient pokerrpc.PokerServiceClient) {
	model := NewModel(ctx, clientID, lobbyClient, pokerClient)

	p := tea.NewProgram(model, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		log.Fatalf("Error running UI: %v", err)
	}
}
