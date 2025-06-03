package ui

import (
	"context"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vctt94/poker-bisonrelay/pkg/client"
	"github.com/vctt94/poker-bisonrelay/pkg/rpc/grpc/pokerrpc"
	"google.golang.org/grpc/metadata"
)

// Event-driven message types matching proto notifications
type notificationMsg *pokerrpc.Notification
type errorMsg error
type tickMsg struct{}

// Legacy message types for specific data (can be refactored to use notifications)
type tablesMsg []*pokerrpc.Table
type gameUpdateMsg *pokerrpc.GameUpdate

// CommandDispatcher dispatches commands from the UI to backend services
type CommandDispatcher struct {
	ctx      context.Context
	clientID string
	pc       *client.PokerClient
}

// NewCommandDispatcher creates a new command dispatcher for the UI
func NewCommandDispatcher(ctx context.Context, clientID string, pc *client.PokerClient) *CommandDispatcher {
	return &CommandDispatcher{
		ctx:      ctx,
		clientID: clientID,
		pc:       pc,
	}
}

// Command methods on the dispatcher

func (d *CommandDispatcher) getBalanceCmd() tea.Cmd {
	return func() tea.Msg {
		balance, err := d.pc.GetBalance(d.ctx)
		if err != nil {
			return errorMsg(err)
		}
		// Return as balance updated notification
		return notificationMsg(&pokerrpc.Notification{
			Type:       pokerrpc.NotificationType_BALANCE_UPDATED,
			PlayerId:   d.clientID,
			NewBalance: balance,
			Message:    "Balance retrieved",
		})
	}
}

func (d *CommandDispatcher) getTablesCmd() tea.Cmd {
	return func() tea.Msg {
		tables, err := d.pc.GetTables(d.ctx)
		if err != nil {
			return errorMsg(err)
		}
		return tablesMsg(tables)
	}
}

func (d *CommandDispatcher) joinTableCmd(tableID string) tea.Cmd {
	return func() tea.Msg {
		err := d.pc.JoinTable(d.ctx, tableID)
		if err != nil {
			return errorMsg(err)
		}

		// Return as player joined notification
		return notificationMsg(&pokerrpc.Notification{
			Type:     pokerrpc.NotificationType_PLAYER_JOINED,
			PlayerId: d.clientID,
			TableId:  tableID,
			Message:  "Successfully joined table",
		})
	}
}

func (d *CommandDispatcher) leaveTableCmd() tea.Cmd {
	return func() tea.Msg {
		currentTableID := d.pc.GetCurrentTableID()
		err := d.pc.LeaveTable(d.ctx)
		if err != nil {
			return errorMsg(err)
		}

		// Return as player left notification
		return notificationMsg(&pokerrpc.Notification{
			Type:     pokerrpc.NotificationType_PLAYER_LEFT,
			PlayerId: d.clientID,
			TableId:  currentTableID,
			Message:  "Successfully left table",
		})
	}
}

func (d *CommandDispatcher) createTableCmd(config client.TableCreateConfig) tea.Cmd {
	return func() tea.Msg {
		tableID, err := d.pc.CreateTable(d.ctx, config)
		if err != nil {
			return errorMsg(err)
		}

		// Return as table created notification
		return notificationMsg(&pokerrpc.Notification{
			Type:     pokerrpc.NotificationType_TABLE_CREATED,
			PlayerId: d.clientID,
			TableId:  tableID,
			Message:  "Table created successfully",
		})
	}
}

func (d *CommandDispatcher) setPlayerReadyCmd() tea.Cmd {
	return func() tea.Msg {
		err := d.pc.SetPlayerReady(d.ctx)
		if err != nil {
			return errorMsg(err)
		}

		// Return as player ready notification
		currentTableID := d.pc.GetCurrentTableID()
		notification := &pokerrpc.Notification{
			Type:     pokerrpc.NotificationType_PLAYER_READY,
			PlayerId: d.clientID,
			TableId:  currentTableID,
			Ready:    true,
			Message:  "Player set to ready",
		}

		return notificationMsg(notification)
	}
}

func (d *CommandDispatcher) setPlayerUnreadyCmd() tea.Cmd {
	return func() tea.Msg {
		err := d.pc.SetPlayerUnready(d.ctx)
		if err != nil {
			return errorMsg(err)
		}

		// Return as player unready notification
		currentTableID := d.pc.GetCurrentTableID()
		return notificationMsg(&pokerrpc.Notification{
			Type:     pokerrpc.NotificationType_PLAYER_UNREADY,
			PlayerId: d.clientID,
			TableId:  currentTableID,
			Ready:    false,
			Message:  "Player set to unready",
		})
	}
}

func (d *CommandDispatcher) checkGameStateCmd() tea.Cmd {
	return func() tea.Msg {
		currentTableID := d.pc.GetCurrentTableID()

		// Add player ID to the context metadata
		ctx := metadata.AppendToOutgoingContext(d.ctx, "player-id", d.clientID)

		resp, err := d.pc.PokerService.GetGameState(ctx, &pokerrpc.GetGameStateRequest{
			TableId: currentTableID,
		})
		if err != nil {
			// Check if this is a "table not found" error
			if strings.Contains(err.Error(), "table not found") {
				return notificationMsg(&pokerrpc.Notification{
					Type:    pokerrpc.NotificationType_TABLE_REMOVED,
					TableId: currentTableID,
					Message: "Table no longer exists",
				})
			}
			return errorMsg(err)
		}
		return gameUpdateMsg(resp.GameState)
	}
}

func (d *CommandDispatcher) checkCmd() tea.Cmd {
	return func() tea.Msg {
		currentTableID := d.pc.GetCurrentTableID()
		_, err := d.pc.PokerService.Check(d.ctx, &pokerrpc.CheckRequest{
			PlayerId: d.clientID,
			TableId:  currentTableID,
		})
		if err != nil {
			return errorMsg(err)
		}

		// Return as new round notification (check action)
		return notificationMsg(&pokerrpc.Notification{
			Type:     pokerrpc.NotificationType_NEW_ROUND,
			PlayerId: d.clientID,
			TableId:  currentTableID,
			Message:  "Player checked",
		})
	}
}

func (d *CommandDispatcher) foldCmd() tea.Cmd {
	return func() tea.Msg {
		currentTableID := d.pc.GetCurrentTableID()
		_, err := d.pc.PokerService.Fold(d.ctx, &pokerrpc.FoldRequest{
			PlayerId: d.clientID,
			TableId:  currentTableID,
		})
		if err != nil {
			return errorMsg(err)
		}

		// Return as player folded notification
		return notificationMsg(&pokerrpc.Notification{
			Type:     pokerrpc.NotificationType_PLAYER_FOLDED,
			PlayerId: d.clientID,
			TableId:  currentTableID,
			Message:  "Player folded",
		})
	}
}

func (d *CommandDispatcher) betCmd(amount int64) tea.Cmd {
	return func() tea.Msg {
		currentTableID := d.pc.GetCurrentTableID()
		_, err := d.pc.PokerService.MakeBet(d.ctx, &pokerrpc.MakeBetRequest{
			PlayerId: d.clientID,
			TableId:  currentTableID,
			Amount:   amount,
		})
		if err != nil {
			return errorMsg(err)
		}

		// Return as bet made notification
		return notificationMsg(&pokerrpc.Notification{
			Type:     pokerrpc.NotificationType_BET_MADE,
			PlayerId: d.clientID,
			TableId:  currentTableID,
			Amount:   amount,
			Message:  "Bet placed",
		})
	}
}

// Utility functions
func gameUpdateTicker() tea.Cmd {
	return tea.Tick(time.Second*3, func(t time.Time) tea.Msg {
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
