package main

import (
	"context"
	"fmt"
	"log"
	"strconv"

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
)

// screenState represents the current screen in the UI
type screenState int

const (
	stateMainMenu screenState = iota
	stateTableList
	stateCreateTable
	stateJoinTable
	stateGameLobby
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
	smallBlind string
	bigBlind   string
	minPlayers string
	maxPlayers string
	buyIn      string
	minBalance string

	// For join table
	tableIdInput string
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
		ctx:         ctx,
		clientID:    clientID,
		lobbyClient: lobbyClient,
		pokerClient: pokerClient,
		state:       stateMainMenu,
		menuOptions: menuOptions,
		smallBlind:  "10",
		bigBlind:    "20",
		minPlayers:  "2",
		maxPlayers:  "6",
		buyIn:       "100",
		minBalance:  "100",
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
			if m.state == stateMainMenu {
				m.selectedItem = max(0, m.selectedItem-1)
			} else if m.state == stateTableList && len(m.tables) > 0 {
				m.selectedTable = max(0, m.selectedTable-1)
			}
		case "down", "j":
			if m.state == stateMainMenu {
				m.selectedItem = min(len(m.menuOptions)-1, m.selectedItem+1)
			} else if m.state == stateTableList && len(m.tables) > 0 {
				m.selectedTable = min(len(m.tables)-1, m.selectedTable+1)
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
				minPlayers, _ := strconv.ParseInt(m.minPlayers, 10, 32)
				maxPlayers, _ := strconv.ParseInt(m.maxPlayers, 10, 32)
				buyIn, _ := strconv.ParseInt(m.buyIn, 10, 64)
				minBalance, _ := strconv.ParseInt(m.minBalance, 10, 64)

				cmds = append(cmds, createTableCmd(
					m.ctx,
					m.clientID,
					smallBlind,
					bigBlind,
					int32(minPlayers),
					int32(maxPlayers),
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

	case playerUnreadyMsg:
		m.message = "You are now unready"
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

	// Display form values
	s += fmt.Sprintf("Small Blind: %s\n", m.smallBlind)
	s += fmt.Sprintf("Big Blind: %s\n", m.bigBlind)
	s += fmt.Sprintf("Min Players: %s\n", m.minPlayers)
	s += fmt.Sprintf("Max Players: %s\n", m.maxPlayers)
	s += fmt.Sprintf("Buy In: %s\n", m.buyIn)
	s += fmt.Sprintf("Min Balance: %s\n", m.minBalance)

	s += "\n" + helpStyle.Render("Enter: Create Table with these values • Esc: Back")

	return s
}

func (m Model) viewJoinTable() string {
	var s string

	s += titleStyle.Render("Join Table") + "\n\n"

	s += "Enter Table ID: " + m.tableIdInput + "\n\n"

	s += helpStyle.Render("Enter: Join Table • Esc: Back")

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

// Custom message types for commands
type balanceMsg int64
type errorMsg error
type tablesMsg []*pokerrpc.Table
type tableJoinedMsg struct{}
type tableLeftMsg struct{}
type tableCreatedMsg string
type playerReadyMsg struct{}
type playerUnreadyMsg struct{}

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

// RunUI starts the Bubble Tea UI
func RunUI(ctx context.Context, clientID string, lobbyClient pokerrpc.LobbyServiceClient, pokerClient pokerrpc.PokerServiceClient) {
	p := tea.NewProgram(initialModel(ctx, clientID, lobbyClient, pokerClient), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		log.Fatal("Error running program:", err)
	}
}
