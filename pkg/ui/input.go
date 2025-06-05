package ui

import (
	"fmt"
	"strconv"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vctt94/poker-bisonrelay/pkg/client"
)

// InputHandler handles input processing for different UI states
type InputHandler struct {
	ui *PokerUI
}

// HandleKeyMsg processes keyboard input based on current state
func (ih *InputHandler) HandleKeyMsg(msg tea.KeyMsg) tea.Cmd {
	switch ih.ui.state {
	case stateMainMenu:
		return ih.handleMainMenuInput(msg)
	case stateTableList:
		return ih.handleTableListInput(msg)
	case stateCreateTable:
		return ih.handleCreateTableInput(msg)
	case stateJoinTable:
		return ih.handleJoinTableInput(msg)
	case stateGameLobby:
		return ih.handleGameLobbyInput(msg)
	case stateActiveGame:
		return ih.handleActiveGameInput(msg)
	case stateBetInput:
		return ih.handleBetInputInput(msg)
	}
	return nil
}

// handleMainMenuInput processes input for the main menu
func (ih *InputHandler) handleMainMenuInput(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "q", "ctrl+c":
		return tea.Quit
	case "up", "k":
		if ih.ui.selectedItem > 0 {
			ih.ui.selectedItem--
		}
	case "down", "j":
		if ih.ui.selectedItem < len(ih.ui.menuOptions)-1 {
			ih.ui.selectedItem++
		}
	case "enter", " ":
		switch ih.ui.menuOptions[ih.ui.selectedItem] {
		case "Return to Table":
			// Return to the game lobby or active game based on current table state
			currentTableID := ih.ui.pc.GetCurrentTableID()
			if currentTableID != "" {
				ih.ui.state = stateGameLobby
				ih.ui.updateMenuOptionsForGameState()
			}
		case optionListTables:
			return ih.ui.dispatcher.getTablesCmd()
		case optionCreateTable:
			ih.ui.state = stateCreateTable
		case optionJoinTable:
			ih.ui.state = stateJoinTable
		case optionCheckBalance:
			return ih.ui.dispatcher.getBalanceCmd()
		case optionQuit:
			return tea.Quit
		}
	}
	return nil
}

// handleTableListInput processes input for the table list
func (ih *InputHandler) handleTableListInput(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "q", "esc":
		ih.ui.state = stateMainMenu
		ih.ui.selectedItem = 0 // Reset to first option when going back to main menu
		ih.ui.updateMenuOptionsForGameState()
	case "up", "k":
		if ih.ui.selectedTable > 0 {
			ih.ui.selectedTable--
		}
	case "down", "j":
		if ih.ui.selectedTable < len(ih.ui.tables)-1 {
			ih.ui.selectedTable++
		}
	case "enter", " ":
		if len(ih.ui.tables) > 0 {
			selectedTable := ih.ui.tables[ih.ui.selectedTable]
			return ih.ui.dispatcher.joinTableCmd(selectedTable.Id)
		}
	}
	return nil
}

// handleCreateTableInput processes input for the create table form
func (ih *InputHandler) handleCreateTableInput(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "q":
		ih.ui.state = stateMainMenu
		ih.ui.selectedItem = 0 // Reset to first option when going back to main menu
		ih.ui.updateMenuOptionsForGameState()
	case "up", "k":
		if ih.ui.selectedFormField > 0 {
			ih.ui.selectedFormField--
		}
	case "down", "j":
		if ih.ui.selectedFormField < 5 {
			ih.ui.selectedFormField++
		}
	case "enter":
		// Parse form values and create table config
		smallBlind, _ := strconv.ParseInt(ih.ui.smallBlind, 10, 64)
		bigBlind, _ := strconv.ParseInt(ih.ui.bigBlind, 10, 64)
		requiredPlayers, _ := strconv.ParseInt(ih.ui.requiredPlayers, 10, 32)
		buyIn, _ := strconv.ParseInt(ih.ui.buyIn, 10, 64)
		minBalance, _ := strconv.ParseInt(ih.ui.minBalance, 10, 64)
		startingChips, _ := strconv.ParseInt(ih.ui.startingChips, 10, 64)

		config := client.TableCreateConfig{
			SmallBlind:    smallBlind,
			BigBlind:      bigBlind,
			MinPlayers:    int32(requiredPlayers),
			MaxPlayers:    int32(requiredPlayers), // Using same value for min and max for now
			BuyIn:         buyIn,
			MinBalance:    minBalance,
			StartingChips: startingChips,
		}
		return ih.ui.dispatcher.createTableCmd(config)
	case "backspace":
		return ih.handleCreateTableBackspace()
	default:
		// Handle number input
		if len(msg.String()) == 1 && msg.String()[0] >= '0' && msg.String()[0] <= '9' {
			return ih.handleCreateTableNumberInput(msg.String())
		}
	}
	return nil
}

// handleCreateTableBackspace handles backspace input for create table form
func (ih *InputHandler) handleCreateTableBackspace() tea.Cmd {
	switch ih.ui.selectedFormField {
	case 0: // Small Blind
		if len(ih.ui.smallBlind) > 0 {
			ih.ui.smallBlind = ih.ui.smallBlind[:len(ih.ui.smallBlind)-1]
		}
	case 1: // Big Blind
		if len(ih.ui.bigBlind) > 0 {
			ih.ui.bigBlind = ih.ui.bigBlind[:len(ih.ui.bigBlind)-1]
		}
	case 2: // Required Players
		if len(ih.ui.requiredPlayers) > 0 {
			ih.ui.requiredPlayers = ih.ui.requiredPlayers[:len(ih.ui.requiredPlayers)-1]
		}
	case 3: // Buy In
		if len(ih.ui.buyIn) > 0 {
			ih.ui.buyIn = ih.ui.buyIn[:len(ih.ui.buyIn)-1]
		}
	case 4: // Min Balance
		if len(ih.ui.minBalance) > 0 {
			ih.ui.minBalance = ih.ui.minBalance[:len(ih.ui.minBalance)-1]
		}
	case 5: // Starting Chips
		if len(ih.ui.startingChips) > 0 {
			ih.ui.startingChips = ih.ui.startingChips[:len(ih.ui.startingChips)-1]
		}
	}
	return nil
}

// handleCreateTableNumberInput handles number input for create table form
func (ih *InputHandler) handleCreateTableNumberInput(char string) tea.Cmd {
	switch ih.ui.selectedFormField {
	case 0: // Small Blind
		ih.ui.smallBlind += char
	case 1: // Big Blind
		ih.ui.bigBlind += char
	case 2: // Required Players
		ih.ui.requiredPlayers += char
	case 3: // Buy In
		ih.ui.buyIn += char
	case 4: // Min Balance
		ih.ui.minBalance += char
	case 5: // Starting Chips
		ih.ui.startingChips += char
	}
	return nil
}

// handleJoinTableInput processes input for the join table screen
func (ih *InputHandler) handleJoinTableInput(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "q":
		ih.ui.state = stateMainMenu
		ih.ui.selectedItem = 0 // Reset to first option when going back to main menu
		ih.ui.updateMenuOptionsForGameState()
	case "enter":
		if ih.ui.tableIdInput != "" {
			return ih.ui.dispatcher.joinTableCmd(ih.ui.tableIdInput)
		}
	case "backspace":
		if len(ih.ui.tableIdInput) > 0 {
			ih.ui.tableIdInput = ih.ui.tableIdInput[:len(ih.ui.tableIdInput)-1]
		}
	default:
		// Handle number input
		if len(msg.String()) == 1 && msg.String()[0] >= '0' && msg.String()[0] <= '9' {
			ih.ui.tableIdInput += msg.String()
		}
	}
	return nil
}

// handleGameLobbyInput processes input for the game lobby
func (ih *InputHandler) handleGameLobbyInput(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "q":
		// Go back to main menu instead of quitting
		ih.ui.resetToMainMenu()
		return nil
	case "ctrl+c":
		return tea.Quit
	case "up", "k":
		if ih.ui.selectedItem > 0 {
			ih.ui.selectedItem--
		}
	case "down", "j":
		if ih.ui.selectedItem < len(ih.ui.menuOptions)-1 {
			ih.ui.selectedItem++
		}
	case "enter", " ":
		switch ih.ui.menuOptions[ih.ui.selectedItem] {
		case optionSetReady:
			return ih.ui.dispatcher.setPlayerReadyCmd()
		case optionSetUnready:
			return ih.ui.dispatcher.setPlayerUnreadyCmd()
		case optionCheckBalance:
			return ih.ui.dispatcher.getBalanceCmd()
		case optionLeaveTable:
			return ih.ui.dispatcher.leaveTableCmd()
		}
	}
	return nil
}

// handleActiveGameInput processes input for the active game
func (ih *InputHandler) handleActiveGameInput(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "q":
		// Go back to main menu instead of quitting
		ih.ui.resetToMainMenu()
		return nil
	case "ctrl+c":
		return tea.Quit
	case "up", "k":
		if ih.ui.selectedItem > 0 {
			ih.ui.selectedItem--
		}
	case "down", "j":
		if ih.ui.selectedItem < len(ih.ui.menuOptions)-1 {
			ih.ui.selectedItem++
		}
	case "enter", " ":
		if len(ih.ui.menuOptions) > ih.ui.selectedItem {
			switch ih.ui.menuOptions[ih.ui.selectedItem] {
			case optionCheck:
				return ih.ui.dispatcher.checkCmd()
			case optionBet:
				ih.ui.state = stateBetInput
				// Calculate minimum bet for poker rules:
				// - To call: match the current bet
				// - To raise: current bet + big blind
				bigBlind := ih.ui.GetCurrentTableBigBlind()
				currentBet := ih.ui.currentBet

				var minBet int64
				if currentBet == 0 {
					// No one has bet yet, minimum is big blind
					minBet = bigBlind
				} else {
					// Someone has bet, minimum to call is the current bet
					// Minimum to raise is current bet + big blind
					minBet = currentBet
				}
				ih.ui.betAmount = fmt.Sprintf("%d", minBet)
			case optionFold:
				return ih.ui.dispatcher.foldCmd()
			case optionLeaveTable:
				return ih.ui.dispatcher.leaveTableCmd()
			}
		}
	}
	return nil
}

// handleBetInputInput processes input for the bet input screen
func (ih *InputHandler) handleBetInputInput(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "q":
		ih.ui.state = stateActiveGame
	case "enter":
		if ih.ui.betAmount != "" {
			if amount, err := strconv.ParseInt(ih.ui.betAmount, 10, 64); err == nil {
				return ih.ui.dispatcher.betCmd(amount)
			}
		}
	case "backspace":
		if len(ih.ui.betAmount) > 0 {
			ih.ui.betAmount = ih.ui.betAmount[:len(ih.ui.betAmount)-1]
		}
	default:
		// Handle number input
		if len(msg.String()) == 1 && msg.String()[0] >= '0' && msg.String()[0] <= '9' {
			ih.ui.betAmount += msg.String()
		}
	}
	return nil
}
