package ui

import (
	"context"
	"fmt"
	"log"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vctt94/poker-bisonrelay/pkg/client"
	"github.com/vctt94/poker-bisonrelay/pkg/rpc/grpc/pokerrpc"
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
	stateBetInput   // New state for entering bet amount
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
	startingChips     string
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

	// For betting input
	betAmount string

	// Component handlers
	dispatcher   *CommandDispatcher
	inputHandler *InputHandler
	renderer     *Renderer

	// New fields
	myTurn bool

	// current account balance
	balance int64
}

// NewPokerUI creates a new poker UI model
func NewPokerUI(ctx context.Context, client *client.PokerClient) *PokerUI {
	// Initial menu options
	menuOptions := []menuOption{
		optionListTables,
		optionCreateTable,
		optionJoinTable,
		optionCheckBalance,
		optionQuit,
	}

	ui := &PokerUI{
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
		startingChips:   "1000",
	}

	// Create component handlers
	ui.dispatcher = &CommandDispatcher{
		ctx:      ctx,
		clientID: client.ID,
		pc:       client,
	}
	ui.inputHandler = &InputHandler{ui: ui}
	ui.renderer = &Renderer{ui: ui}

	return ui
}

func (m *PokerUI) Init() tea.Cmd {
	return m.dispatcher.getBalanceCmd()
}

func (m *PokerUI) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Handle input through the input handler
		cmd = m.inputHandler.HandleKeyMsg(msg)

	case tablesMsg:
		m.tables = []*pokerrpc.Table(msg)
		m.state = stateTableList
		m.selectedTable = 0
		m.err = nil

	case notificationMsg:
		notif := (*pokerrpc.Notification)(msg)
		m.message = notif.Message
		m.err = nil
		// Update cached balance if this is a balance notification
		if notif.Type == pokerrpc.NotificationType_BALANCE_UPDATED {
			m.balance = notif.NewBalance
		}
		// Process the notification to handle state transitions
		cmd = m.handleNotification(notif)

	case *pokerrpc.Notification:
		// Handle direct notification messages from client
		notif := msg
		m.message = notif.Message
		m.err = nil
		// Update cached balance if this is a balance notification
		if notif.Type == pokerrpc.NotificationType_BALANCE_UPDATED {
			m.balance = notif.NewBalance
		}
		// Process the notification to handle state transitions
		cmd = m.handleNotification(notif)

	case client.GameUpdateMsg:
		gameUpdate := (*pokerrpc.GameUpdate)(msg)

		// Update current player ID
		m.currentPlayerID = gameUpdate.CurrentPlayer

		// Handle state updates from the game
		m.gamePhase = gameUpdate.Phase
		m.players = gameUpdate.Players
		m.communityCards = gameUpdate.CommunityCards
		m.pot = gameUpdate.Pot
		m.currentBet = gameUpdate.CurrentBet

		// Count joined players
		m.playersJoined = int32(len(m.players))
		// Set required players from game update
		m.playersRequired = gameUpdate.PlayersRequired

		// Update current turn based on game update
		if m.currentPlayerID == m.clientID && (gameUpdate.Phase == pokerrpc.GamePhase_PRE_FLOP ||
			gameUpdate.Phase == pokerrpc.GamePhase_FLOP ||
			gameUpdate.Phase == pokerrpc.GamePhase_TURN ||
			gameUpdate.Phase == pokerrpc.GamePhase_RIVER ||
			gameUpdate.Phase == pokerrpc.GamePhase_SHOWDOWN) {
			m.myTurn = true
		} else {
			m.myTurn = false
		}

		// Determine current UI state based on game phase
		switch gameUpdate.Phase {
		case pokerrpc.GamePhase_WAITING:
			m.state = stateGameLobby
		default:
			m.state = stateActiveGame
		}

		// Update menu options for the current state
		m.updateMenuOptionsForGameState()

		m.err = nil

	case errorMsg:
		m.err = error(msg)
		m.message = ""

	}

	return m, cmd
}

// handleNotification processes notifications from the backend
func (m *PokerUI) handleNotification(notification *pokerrpc.Notification) tea.Cmd {
	switch notification.Type {
	case pokerrpc.NotificationType_BALANCE_UPDATED:
		m.message = fmt.Sprintf("Balance: %d", notification.NewBalance)
		m.balance = notification.NewBalance
		return nil

	case pokerrpc.NotificationType_PLAYER_JOINED:
		if notification.PlayerId == m.clientID {
			m.state = stateGameLobby
			m.message = fmt.Sprintf("Joined table %s", notification.TableId)
			m.updateMenuOptionsForGameState()
			return nil
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
			return nil
		}

	case pokerrpc.NotificationType_TABLE_REMOVED:
		currentTableID := m.pc.GetCurrentTableID()
		if notification.TableId == currentTableID {
			m.resetToMainMenu()
			m.message = "Table was closed"
			return nil
		}

	case pokerrpc.NotificationType_SMALL_BLIND_POSTED:
		m.message = fmt.Sprintf("Small blind posted: %d chips by %s", notification.Amount, notification.PlayerId)
		// Game updates are now received via stream
		return nil

	case pokerrpc.NotificationType_BIG_BLIND_POSTED:
		m.message = fmt.Sprintf("Big blind posted: %d chips by %s", notification.Amount, notification.PlayerId)
		// Game updates are now received via stream
		return nil

	case pokerrpc.NotificationType_PLAYER_READY:
		if notification.PlayerId == m.clientID {
			m.message = "You are now ready"
		} else {
			m.message = fmt.Sprintf("%s is now ready", notification.PlayerId)
		}
		return nil

	case pokerrpc.NotificationType_PLAYER_UNREADY:
		if notification.PlayerId == m.clientID {
			m.message = "You are now unready"
		} else {
			m.message = fmt.Sprintf("%s is no longer ready", notification.PlayerId)
		}
		return nil

	case pokerrpc.NotificationType_ALL_PLAYERS_READY:
		m.message = "All players are ready! Game starting soon..."
		return nil

	case pokerrpc.NotificationType_GAME_STARTED:
		m.state = stateActiveGame
		m.message = "Game started!"
		m.updateMenuOptionsForGameState()
		return nil

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
func (m *PokerUI) View() string {
	var s string

	// Show any temporary message
	if m.message != "" {
		s += TitleStyle.Render(m.message) + "\n\n"
	}

	// Show error if any
	if m.err != nil {
		s += ErrorStyle.Render(fmt.Sprintf("Error: %v", m.err)) + "\n\n"
	}

	switch m.state {
	case stateMainMenu:
		s += m.renderer.RenderMainMenu()

	case stateTableList:
		s += m.renderer.RenderTableList()

	case stateCreateTable:
		s += m.renderer.RenderCreateTable()

	case stateJoinTable:
		s += m.renderer.RenderJoinTable()

	case stateGameLobby:
		s += m.renderer.RenderGameLobby()

	case stateActiveGame:
		s += m.renderer.RenderActiveGame()

	case stateBetInput:
		s += m.renderer.RenderBetInput()
	}

	s += "\n" + HelpStyle.Render("Press 'q' to go back/quit, Ctrl+C to force quit")
	return s
}

// Run starts the UI
func Run(ctx context.Context, client *client.PokerClient) {
	model := NewPokerUI(ctx, client)

	p := tea.NewProgram(model, tea.WithAltScreen())

	// Start a goroutine to listen for updates from the poker client
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case msg := <-client.UpdatesCh:
				p.Send(msg)
			case err := <-client.ErrorsCh:
				p.Send(errorMsg(err))
			}
		}
	}()

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
	m.selectedItem = 0 // Explicitly reset when changing to main menu
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
		// Only reset selectedItem if we're actually changing to main menu
		// or if the menu options structure changes significantly
		oldOptions := m.menuOptions
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

		// Only reset selectedItem if menu structure changed or it's out of bounds
		if len(oldOptions) != len(m.menuOptions) || m.selectedItem >= len(m.menuOptions) {
			m.selectedItem = 0
		}
	} else if m.state == stateGameLobby {
		oldOptions := m.menuOptions
		m.menuOptions = []menuOption{
			optionSetReady,
			optionSetUnready,
			optionLeaveTable,
			optionCheckBalance,
			optionQuit,
		}

		// Only reset selectedItem if menu structure changed or it's out of bounds
		if len(oldOptions) != len(m.menuOptions) || m.selectedItem >= len(m.menuOptions) {
			m.selectedItem = 0
		}
	} else if m.state == stateActiveGame {
		oldOptions := m.menuOptions
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

		// Only reset selectedItem if menu structure changed or it's out of bounds
		if len(oldOptions) != len(m.menuOptions) || m.selectedItem >= len(m.menuOptions) {
			m.selectedItem = 0
		}
	}
}
