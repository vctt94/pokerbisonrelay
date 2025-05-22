package main

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/vctt94/poker-bisonrelay/rpc/grpc/pokerrpc"
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
	myCards         []*pokerrpc.Card
}

func initialModel(ctx context.Context, clientID string, lobbyClient pokerrpc.LobbyServiceClient, pokerClient pokerrpc.PokerServiceClient) Model {
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
				case optionCheckBalance:
					cmds = append(cmds, getBalanceCmd(m.ctx, m.clientID, m.lobbyClient))
				case optionQuit:
					return m, tea.Quit
				}
			case stateTableList:
				if len(m.tables) > 0 {
					selectedTable := m.tables[m.selectedTable]
					m.tableID = selectedTable.Id
					cmds = append(cmds, joinTableCmd(m.ctx, m.clientID, m.tableID, m.lobbyClient))
				}
			case stateCreateTable:
				// Parse table creation inputs
				smallBlind, _ := strconv.ParseInt(m.smallBlind, 10, 64)
				bigBlind, _ := strconv.ParseInt(m.bigBlind, 10, 64)
				requiredPlayers, _ := strconv.ParseInt(m.requiredPlayers, 10, 32)
				buyIn, _ := strconv.ParseInt(m.buyIn, 10, 64)
				minBalance, _ := strconv.ParseInt(m.minBalance, 10, 64)

				cmds = append(cmds, createTableCmd(
					m.ctx,
					m.clientID,
					smallBlind,
					bigBlind,
					int32(requiredPlayers),
					int32(requiredPlayers),
					buyIn,
					minBalance,
					m.lobbyClient,
				))
			case stateJoinTable:
				if m.tableIdInput != "" {
					m.tableID = m.tableIdInput
					cmds = append(cmds, joinTableCmd(m.ctx, m.clientID, m.tableID, m.lobbyClient))
				}
			}
		case "esc":
			// Go back to main menu
			m.state = stateMainMenu
		case "backspace":
			if m.state == stateCreateTable {
				switch m.selectedFormField {
				case 0:
					if len(m.smallBlind) > 0 {
						m.smallBlind = m.smallBlind[:len(m.smallBlind)-1]
					}
				case 1:
					if len(m.bigBlind) > 0 {
						m.bigBlind = m.bigBlind[:len(m.bigBlind)-1]
					}
				case 2:
					if len(m.requiredPlayers) > 0 {
						m.requiredPlayers = m.requiredPlayers[:len(m.requiredPlayers)-1]
					}
				case 3:
					if len(m.buyIn) > 0 {
						m.buyIn = m.buyIn[:len(m.buyIn)-1]
					}
				case 4:
					if len(m.minBalance) > 0 {
						m.minBalance = m.minBalance[:len(m.minBalance)-1]
					}
				}
			} else if m.state == stateJoinTable {
				if len(m.tableIdInput) > 0 {
					m.tableIdInput = m.tableIdInput[:len(m.tableIdInput)-1]
				}
			}
		default:
			// Handle text input for form fields
			if m.state == stateCreateTable && msg.Type == tea.KeyRunes {
				// Only allow digits
				r := msg.Runes[0]
				if r >= '0' && r <= '9' {
					switch m.selectedFormField {
					case 0:
						m.smallBlind += string(r)
					case 1:
						m.bigBlind += string(r)
					case 2:
						m.requiredPlayers += string(r)
					case 3:
						m.buyIn += string(r)
					case 4:
						m.minBalance += string(r)
					}
				}
			} else if m.state == stateJoinTable && msg.Type == tea.KeyRunes {
				// Allow alphanumeric and hyphen for table IDs
				m.tableIdInput += string(msg.Runes)
			}
		}

	case balanceMsg:
		m.balance = int64(msg)
		m.message = fmt.Sprintf("Current balance: %d", m.balance)

	case errorMsg:
		m.err = msg
		m.message = fmt.Sprintf("Error: %v", m.err)

	case tablesMsg:
		m.tables = msg
		if len(m.tables) == 0 {
			m.message = "No tables available"
		} else {
			m.message = fmt.Sprintf("Found %d tables", len(m.tables))
		}

	case tableJoinedMsg:
		m.state = stateGameLobby
		m.message = fmt.Sprintf("Joined table %s", m.tableID)

		// Update menu options for game lobby
		m.menuOptions = []menuOption{
			optionSetReady,
			optionSetUnready,
			optionLeaveTable,
			optionCheckBalance,
			optionQuit,
		}

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
		m.state = stateMainMenu
		m.message = fmt.Sprintf("Created table %s", string(msg))
		m.tableID = string(msg)

	case playerReadyMsg:
		m.message = "You are now ready"
		// Start polling for game updates
		cmds = append(cmds, gameUpdateTicker())

	case playerUnreadyMsg:
		m.message = "You are now unready"

	case gameStartedMsg:
		// Transition to active game state when game starts
		m.state = stateActiveGame
		m.message = "Game has started!"

		// Continue polling for updates during the game
		cmds = append(cmds, gameUpdateTicker())

	case tickMsg:
		// Only poll if in game lobby or active game
		if m.state == stateGameLobby || m.state == stateActiveGame {
			cmds = append(cmds, checkGameStateCmd(m.ctx, m.clientID, m.tableID, m.pokerClient))
		}

	case gameUpdateMsg:
		// Process game update
		gameUpdate := pokerrpc.GameUpdate(*msg)

		// Update our game state
		m.gamePhase = gameUpdate.Phase
		m.pot = gameUpdate.Pot
		m.currentBet = gameUpdate.CurrentBet
		m.players = gameUpdate.Players
		m.communityCards = gameUpdate.CommunityCards
		m.currentPlayerID = gameUpdate.CurrentPlayer

		// If game started and we're still in lobby, transition to game state
		if gameUpdate.GameStarted && m.state == stateGameLobby {
			m.state = stateActiveGame
			m.message = "Game has started!"
		}

		// Update menu options based on whether it's the player's turn
		updateMenuOptionsForGameState(&m)

		// Continue polling
		cmds = append(cmds, gameUpdateTicker())
	}

	return m, tea.Batch(cmds...)
}

func (m Model) View() string {
	switch m.state {
	case stateMainMenu:
		return m.viewMainMenu()
	case stateTableList:
		return m.viewTableList()
	case stateCreateTable:
		return m.viewCreateTable()
	case stateJoinTable:
		return m.viewJoinTable()
	case stateGameLobby:
		return m.viewGameLobby()
	case stateActiveGame:
		return m.viewActiveGame()
	default:
		return "Unknown state"
	}
}

func (m Model) viewMainMenu() string {
	var s string

	s += titleStyle.Render("Poker Game Lobby") + "\n\n"

	// Display balance
	s += gameInfoStyle.Render(fmt.Sprintf("Balance: %d", m.balance)) + "\n\n"

	// Display temporary message if any
	if m.message != "" {
		s += gameInfoStyle.Render(m.message) + "\n\n"
	}

	// Menu options
	for i, option := range m.menuOptions {
		cursor := " "
		if m.selectedItem == i {
			cursor = ">"
			s += focusedStyle.Render(fmt.Sprintf("%s %s", cursor, option)) + "\n"
		} else {
			s += blurredStyle.Render(fmt.Sprintf("%s %s", cursor, option)) + "\n"
		}
	}

	// Help text
	s += "\n" + helpStyle.Render("↑/↓: Navigate • Enter: Select • q: Quit")

	return s
}

func (m Model) viewTableList() string {
	var s string

	s += titleStyle.Render("Available Tables") + "\n\n"

	if len(m.tables) == 0 {
		s += "No tables available.\n"
	} else {
		for i, table := range m.tables {
			status := "Waiting"
			if table.GameStarted {
				status = "In Progress"
			} else if table.AllPlayersReady {
				status = "Ready to Start"
			}

			// Highlight selected table
			line := fmt.Sprintf("%d. ID: %s, Players: %d/%d, Blinds: %d/%d, Status: %s",
				i+1, table.Id, table.CurrentPlayers, table.MaxPlayers, table.SmallBlind, table.BigBlind, status)

			if i == m.selectedTable {
				s += focusedStyle.Render("> "+line) + "\n"
			} else {
				s += "  " + line + "\n"
			}
		}
	}

	s += "\n" + helpStyle.Render("↑/↓: Navigate • Enter: Join Selected Table • Esc: Back")

	return s
}

func (m Model) viewCreateTable() string {
	var s string

	s += titleStyle.Render("Create New Table") + "\n\n"

	// Display form values with highlighting for selected field
	formFields := []struct {
		label string
		value string
		index int
	}{
		{"Small Blind", m.smallBlind, 0},
		{"Big Blind", m.bigBlind, 1},
		{"Required Players", m.requiredPlayers, 2},
		{"Buy In", m.buyIn, 3},
		{"Min Balance", m.minBalance, 4},
	}

	for _, field := range formFields {
		prefix := "  "
		if field.index == m.selectedFormField {
			prefix = "> "
			s += focusedStyle.Render(fmt.Sprintf("%s%s: %s", prefix, field.label, field.value))
			s += focusedStyle.Render("▋") + "\n"
		} else {
			s += fmt.Sprintf("%s%s: %s\n", prefix, field.label, field.value)
		}
	}

	s += "\n" + helpStyle.Render("↑/↓: Select Field • Type Numbers to Edit • Enter: Create Table • Esc: Back")

	return s
}

func (m Model) viewJoinTable() string {
	var s string

	s += titleStyle.Render("Join Table") + "\n\n"

	// Display the input field with a highlight
	s += "Enter Table ID: "
	s += focusedStyle.Render(m.tableIdInput+"▋") + "\n\n"

	s += helpStyle.Render("Type to edit • Enter: Join Table • Esc: Back")

	return s
}

func (m Model) viewGameLobby() string {
	var s string

	s += titleStyle.Render("Game Lobby") + "\n\n"

	s += gameInfoStyle.Render(fmt.Sprintf("Table ID: %s", m.tableID)) + "\n"
	s += gameInfoStyle.Render(fmt.Sprintf("Balance: %d", m.balance)) + "\n\n"

	// Display temporary message if any
	if m.message != "" {
		s += gameInfoStyle.Render(m.message) + "\n\n"
	}

	// Menu options
	for i, option := range m.menuOptions {
		cursor := " "
		if m.selectedItem == i {
			cursor = ">"
			s += focusedStyle.Render(fmt.Sprintf("%s %s", cursor, option)) + "\n"
		} else {
			s += blurredStyle.Render(fmt.Sprintf("%s %s", cursor, option)) + "\n"
		}
	}

	// Help text
	s += "\n" + helpStyle.Render("↑/↓: Navigate • Enter: Select • q: Quit")

	return s
}

func (m Model) viewActiveGame() string {
	var s string

	s += titleStyle.Render("Poker Game in Progress") + "\n\n"

	// Display table info
	s += gameInfoStyle.Render(fmt.Sprintf("Table: %s", m.tableID)) + "\n"
	s += gameInfoStyle.Render(fmt.Sprintf("Your Balance: %d", m.balance)) + "\n"

	// Display game phase
	phase := "Unknown"
	switch m.gamePhase {
	case pokerrpc.GamePhase_WAITING:
		phase = "Waiting for players"
	case pokerrpc.GamePhase_PRE_FLOP:
		phase = "Pre-Flop"
	case pokerrpc.GamePhase_FLOP:
		phase = "Flop"
	case pokerrpc.GamePhase_TURN:
		phase = "Turn"
	case pokerrpc.GamePhase_RIVER:
		phase = "River"
	case pokerrpc.GamePhase_SHOWDOWN:
		phase = "Showdown"
	}
	s += gameInfoStyle.Render(fmt.Sprintf("Phase: %s", phase)) + "\n"

	// Display pot and current bet
	s += gameInfoStyle.Render(fmt.Sprintf("Pot: %d • Current Bet: %d", m.pot, m.currentBet)) + "\n\n"

	// Display players
	s += "Players:\n"
	for _, p := range m.players {
		playerStatus := ""
		if p.Id == m.currentPlayerID {
			playerStatus = " (Current Turn)"
		}
		if p.Folded {
			playerStatus += " (Folded)"
		}

		playerName := p.Id
		if p.Id == m.clientID {
			playerName += " (You)"
		}

		s += fmt.Sprintf("• %s - Balance: %d%s\n", playerName, p.Balance, playerStatus)
	}
	s += "\n"

	// Display community cards
	if len(m.communityCards) > 0 {
		s += "Community Cards: "
		for _, card := range m.communityCards {
			s += fmt.Sprintf("%s%s ", card.Value, card.Suit)
		}
		s += "\n\n"
	}

	// Display temporary message if any
	if m.message != "" {
		s += gameInfoStyle.Render(m.message) + "\n\n"
	}

	// Menu options
	for i, option := range m.menuOptions {
		cursor := " "
		if m.selectedItem == i {
			cursor = ">"
			s += focusedStyle.Render(fmt.Sprintf("%s %s", cursor, option)) + "\n"
		} else {
			s += blurredStyle.Render(fmt.Sprintf("%s %s", cursor, option)) + "\n"
		}
	}

	// Help text
	s += "\n" + helpStyle.Render("↑/↓: Navigate • Enter: Select • q: Quit")

	return s
}

// Custom message types for commands
type balanceMsg int64
type errorMsg error
type tablesMsg []*pokerrpc.Table
type tableJoinedMsg struct{}
type tableLeftMsg struct{}
type tableCreatedMsg string
type playerReadyMsg struct{}
type playerUnreadyMsg struct{}
type gameStartedMsg struct{}
type gameUpdateMsg *pokerrpc.GameUpdate

// Standalone command functions that return tea.Cmd
func getBalanceCmd(ctx context.Context, clientID string, lobbyClient pokerrpc.LobbyServiceClient) tea.Cmd {
	return func() tea.Msg {
		balanceResp, err := lobbyClient.GetBalance(ctx, &pokerrpc.GetBalanceRequest{
			PlayerId: clientID,
		})
		if err != nil {
			return errorMsg(fmt.Errorf("failed to get balance: %v", err))
		}
		return balanceMsg(balanceResp.Balance)
	}
}

func getTablesCmd(ctx context.Context, lobbyClient pokerrpc.LobbyServiceClient) tea.Cmd {
	return func() tea.Msg {
		resp, err := lobbyClient.GetTables(ctx, &pokerrpc.GetTablesRequest{})
		if err != nil {
			return errorMsg(fmt.Errorf("failed to get tables: %v", err))
		}
		return tablesMsg(resp.Tables)
	}
}

func joinTableCmd(ctx context.Context, clientID string, tableID string, lobbyClient pokerrpc.LobbyServiceClient) tea.Cmd {
	return func() tea.Msg {
		_, err := lobbyClient.JoinTable(ctx, &pokerrpc.JoinTableRequest{
			PlayerId: clientID,
			TableId:  tableID,
		})
		if err != nil {
			return errorMsg(fmt.Errorf("failed to join table: %v", err))
		}
		return tableJoinedMsg{}
	}
}

func leaveTableCmd(ctx context.Context, clientID string, tableID string, lobbyClient pokerrpc.LobbyServiceClient) tea.Cmd {
	return func() tea.Msg {
		_, err := lobbyClient.LeaveTable(ctx, &pokerrpc.LeaveTableRequest{
			PlayerId: clientID,
			TableId:  tableID,
		})
		if err != nil {
			return errorMsg(fmt.Errorf("failed to leave table: %v", err))
		}
		return tableLeftMsg{}
	}
}

func createTableCmd(ctx context.Context, clientID string, smallBlind, bigBlind int64, minPlayers, maxPlayers int32, buyIn, minBalance int64, lobbyClient pokerrpc.LobbyServiceClient) tea.Cmd {
	return func() tea.Msg {
		resp, err := lobbyClient.CreateTable(ctx, &pokerrpc.CreateTableRequest{
			PlayerId:   clientID,
			SmallBlind: smallBlind,
			BigBlind:   bigBlind,
			MinPlayers: minPlayers,
			MaxPlayers: maxPlayers,
			BuyIn:      buyIn,
			MinBalance: minBalance,
		})

		if err != nil {
			return errorMsg(fmt.Errorf("failed to create table: %v", err))
		}

		return tableCreatedMsg(resp.TableId)
	}
}

func setPlayerReadyCmd(ctx context.Context, clientID string, tableID string, lobbyClient pokerrpc.LobbyServiceClient) tea.Cmd {
	return func() tea.Msg {
		_, err := lobbyClient.SetPlayerReady(ctx, &pokerrpc.SetPlayerReadyRequest{
			PlayerId: clientID,
			TableId:  tableID,
		})
		if err != nil {
			return errorMsg(fmt.Errorf("failed to set player ready: %v", err))
		}
		return playerReadyMsg{}
	}
}

func setPlayerUnreadyCmd(ctx context.Context, clientID string, tableID string, lobbyClient pokerrpc.LobbyServiceClient) tea.Cmd {
	return func() tea.Msg {
		_, err := lobbyClient.SetPlayerUnready(ctx, &pokerrpc.SetPlayerUnreadyRequest{
			PlayerId: clientID,
			TableId:  tableID,
		})
		if err != nil {
			return errorMsg(fmt.Errorf("failed to set player unready: %v", err))
		}
		return playerUnreadyMsg{}
	}
}

// Add new message types
type tickMsg struct{}

// Add a tick command to periodically check game state
func checkGameStateCmd(ctx context.Context, clientID, tableID string, pokerClient pokerrpc.PokerServiceClient) tea.Cmd {
	return func() tea.Msg {
		resp, err := pokerClient.GetGameState(ctx, &pokerrpc.GetGameStateRequest{
			TableId: tableID,
		})

		if err != nil {
			return errorMsg(fmt.Errorf("failed to get game state: %v", err))
		}

		if resp.GameState.GameStarted {
			return gameStartedMsg{}
		}

		return gameUpdateMsg(resp.GameState)
	}
}

// A ticker command to poll for game updates
func gameUpdateTicker() tea.Cmd {
	return tea.Tick(1*time.Second, func(time.Time) tea.Msg {
		return tickMsg{}
	})
}

// Helper functions
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Add function to check if it's the player's turn
func isPlayerTurn(currentPlayerID, clientID string) bool {
	return currentPlayerID == clientID
}

// Add commands for poker actions
func checkCmd(ctx context.Context, clientID, tableID string, pokerClient pokerrpc.PokerServiceClient) tea.Cmd {
	return func() tea.Msg {
		_, err := pokerClient.Check(ctx, &pokerrpc.CheckRequest{
			PlayerId: clientID,
			TableId:  tableID,
		})
		if err != nil {
			return errorMsg(fmt.Errorf("failed to check: %v", err))
		}
		return nil
	}
}

func foldCmd(ctx context.Context, clientID, tableID string, pokerClient pokerrpc.PokerServiceClient) tea.Cmd {
	return func() tea.Msg {
		_, err := pokerClient.Fold(ctx, &pokerrpc.FoldRequest{
			PlayerId: clientID,
			TableId:  tableID,
		})
		if err != nil {
			return errorMsg(fmt.Errorf("failed to fold: %v", err))
		}
		return nil
	}
}

func betCmd(ctx context.Context, clientID, tableID string, amount int64, pokerClient pokerrpc.PokerServiceClient) tea.Cmd {
	return func() tea.Msg {
		_, err := pokerClient.MakeBet(ctx, &pokerrpc.MakeBetRequest{
			PlayerId: clientID,
			TableId:  tableID,
			Amount:   amount,
		})
		if err != nil {
			return errorMsg(fmt.Errorf("failed to bet: %v", err))
		}
		return nil
	}
}

// RunUI starts the Bubble Tea UI
func RunUI(ctx context.Context, clientID string, lobbyClient pokerrpc.LobbyServiceClient, pokerClient pokerrpc.PokerServiceClient) {
	p := tea.NewProgram(initialModel(ctx, clientID, lobbyClient, pokerClient), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		log.Fatal("Error running program:", err)
	}
}

// Add a helper function to update the menu options based on game state
func updateMenuOptionsForGameState(m *Model) {
	if m.state == stateActiveGame {
		if isPlayerTurn(m.currentPlayerID, m.clientID) {
			// It's the player's turn - show action buttons
			m.menuOptions = []menuOption{
				optionCheck,
				optionBet,
				optionFold,
				optionLeaveTable,
			}
			m.message = "It's your turn!"
		} else {
			// Not the player's turn - show standard options
			m.menuOptions = []menuOption{
				optionLeaveTable,
				optionCheckBalance,
				optionQuit,
			}
		}
	}
}
