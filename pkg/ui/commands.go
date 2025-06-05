package ui

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vctt94/poker-bisonrelay/pkg/client"
	"github.com/vctt94/poker-bisonrelay/pkg/rpc/grpc/pokerrpc"
)

// Message types
type notificationMsg *pokerrpc.Notification
type errorMsg error

// UI data message types
type tablesMsg []*pokerrpc.Table

// CommandDispatcher handles UI commands and interactions with the poker client
type CommandDispatcher struct {
	ctx      context.Context
	clientID string
	pc       *client.PokerClient
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

func (d *CommandDispatcher) checkCmd() tea.Cmd {
	return func() tea.Msg {
		err := d.pc.Check(d.ctx)
		if err != nil {
			return errorMsg(err)
		}

		// Return as check notification
		return notificationMsg(&pokerrpc.Notification{
			Type:     pokerrpc.NotificationType_BET_MADE,
			PlayerId: d.clientID,
			TableId:  d.pc.GetCurrentTableID(),
			Message:  "Player checked",
		})
	}
}

func (d *CommandDispatcher) foldCmd() tea.Cmd {
	return func() tea.Msg {
		err := d.pc.Fold(d.ctx)
		if err != nil {
			return errorMsg(err)
		}

		// Return as player folded notification
		return notificationMsg(&pokerrpc.Notification{
			Type:     pokerrpc.NotificationType_PLAYER_FOLDED,
			PlayerId: d.clientID,
			TableId:  d.pc.GetCurrentTableID(),
			Message:  "Player folded",
		})
	}
}

func (d *CommandDispatcher) callCmd() tea.Cmd {
	return func() tea.Msg {
		err := d.pc.Call(d.ctx, 0)
		if err != nil {
			return errorMsg(err)
		}

		// Return as call notification
		return notificationMsg(&pokerrpc.Notification{
			Type:     pokerrpc.NotificationType_BET_MADE,
			PlayerId: d.clientID,
			TableId:  d.pc.GetCurrentTableID(),
			Message:  "Player called",
		})
	}
}

func (d *CommandDispatcher) raiseCmd(amount int64) tea.Cmd {
	return func() tea.Msg {
		err := d.pc.Raise(d.ctx, amount)
		if err != nil {
			return errorMsg(err)
		}

		// Return as raise notification
		return notificationMsg(&pokerrpc.Notification{
			Type:     pokerrpc.NotificationType_BET_MADE,
			PlayerId: d.clientID,
			TableId:  d.pc.GetCurrentTableID(),
			Amount:   amount,
			Message:  fmt.Sprintf("Player raised to %d", amount),
		})
	}
}

func (d *CommandDispatcher) betCmd(amount int64) tea.Cmd {
	return func() tea.Msg {
		err := d.pc.Bet(d.ctx, amount)
		if err != nil {
			return errorMsg(err)
		}

		// Return as bet made notification
		return notificationMsg(&pokerrpc.Notification{
			Type:     pokerrpc.NotificationType_BET_MADE,
			PlayerId: d.clientID,
			TableId:  d.pc.GetCurrentTableID(),
			Amount:   amount,
			Message:  "Bet placed",
		})
	}
}

// Utility functions
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
