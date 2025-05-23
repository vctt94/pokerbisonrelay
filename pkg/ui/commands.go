package ui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vctt94/poker-bisonrelay/pkg/rpc/grpc/pokerrpc"
)

// Message types for the UI
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
type tickMsg struct{}

// Helper functions for commands
func getBalanceCmd(ctx context.Context, clientID string, lobbyClient pokerrpc.LobbyServiceClient) tea.Cmd {
	return func() tea.Msg {
		resp, err := lobbyClient.GetBalance(ctx, &pokerrpc.GetBalanceRequest{
			PlayerId: clientID,
		})
		if err != nil {
			return errorMsg(err)
		}
		return balanceMsg(resp.Balance)
	}
}

func getTablesCmd(ctx context.Context, lobbyClient pokerrpc.LobbyServiceClient) tea.Cmd {
	return func() tea.Msg {
		resp, err := lobbyClient.GetTables(ctx, &pokerrpc.GetTablesRequest{})
		if err != nil {
			return errorMsg(err)
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
			return errorMsg(err)
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
			return errorMsg(err)
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
			return errorMsg(err)
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
			return errorMsg(err)
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
			return errorMsg(err)
		}
		return playerUnreadyMsg{}
	}
}

func checkGameStateCmd(ctx context.Context, clientID, tableID string, pokerClient pokerrpc.PokerServiceClient) tea.Cmd {
	return func() tea.Msg {
		resp, err := pokerClient.GetGameState(ctx, &pokerrpc.GetGameStateRequest{
			TableId: tableID,
		})
		if err != nil {
			return errorMsg(err)
		}
		return gameUpdateMsg(resp.GameState)
	}
}

func gameUpdateTicker() tea.Cmd {
	return tea.Tick(time.Second*2, func(t time.Time) tea.Msg {
		return tickMsg{}
	})
}

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

func isPlayerTurn(currentPlayerID, clientID string) bool {
	return currentPlayerID == clientID
}

func checkCmd(ctx context.Context, clientID, tableID string, pokerClient pokerrpc.PokerServiceClient) tea.Cmd {
	return func() tea.Msg {
		_, err := pokerClient.Check(ctx, &pokerrpc.CheckRequest{
			PlayerId: clientID,
			TableId:  tableID,
		})
		if err != nil {
			return errorMsg(err)
		}
		return gameUpdateMsg(nil)
	}
}

func foldCmd(ctx context.Context, clientID, tableID string, pokerClient pokerrpc.PokerServiceClient) tea.Cmd {
	return func() tea.Msg {
		_, err := pokerClient.Fold(ctx, &pokerrpc.FoldRequest{
			PlayerId: clientID,
			TableId:  tableID,
		})
		if err != nil {
			return errorMsg(err)
		}
		return gameUpdateMsg(nil)
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
			return errorMsg(err)
		}
		return gameUpdateMsg(nil)
	}
}

func updateMenuOptionsForGameState(m *Model) {
	if m.state == stateGameLobby {
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
