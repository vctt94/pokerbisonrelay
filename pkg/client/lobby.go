package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/vctt94/pokerbisonrelay/pkg/poker"
	"github.com/vctt94/pokerbisonrelay/pkg/rpc/grpc/pokerrpc"
)

// StartGameStream starts receiving real-time game updates for the current table
func (pc *PokerClient) StartGameStream(ctx context.Context) error {
	pc.gameStreamMu.Lock()
	defer pc.gameStreamMu.Unlock()

	// Don't start if already streaming
	if pc.gameStream != nil {
		return nil
	}

	currentTableID := pc.GetCurrentTableID()
	if currentTableID == "" {
		return fmt.Errorf("not currently at a table")
	}

	// Start the game stream
	stream, err := pc.PokerService.StartGameStream(ctx, &pokerrpc.StartGameStreamRequest{
		PlayerId: pc.ID,
		TableId:  currentTableID,
	})
	if err != nil {
		return fmt.Errorf("failed to start game stream: %w", err)
	}

	pc.gameStream = stream

	// Start goroutine to handle stream updates
	go pc.handleGameStreamUpdates(ctx)

	pc.log.Infof("Started game stream for table %s", currentTableID)
	return nil
}

// CreateTable creates a new poker table using poker.TableConfig
func (pc *PokerClient) CreateTable(ctx context.Context, config poker.TableConfig) (string, error) {
	// Convert poker.TableConfig to RPC CreateTableRequest
	timeBankSeconds := int32(config.TimeBank.Seconds())
	resp, err := pc.LobbyService.CreateTable(ctx, &pokerrpc.CreateTableRequest{
		PlayerId:        pc.ID,
		SmallBlind:      config.SmallBlind,
		BigBlind:        config.BigBlind,
		MaxPlayers:      int32(config.MaxPlayers),
		MinPlayers:      int32(config.MinPlayers),
		MinBalance:      config.MinBalance,
		BuyIn:           config.BuyIn,
		StartingChips:   config.StartingChips,
		TimeBankSeconds: timeBankSeconds,
		AutoStartMs:     int32(config.AutoStartDelay.Milliseconds()),
	})
	if err != nil {
		return "", err
	}

	pc.Lock()
	pc.tableID = resp.TableId
	pc.Unlock()

	// Start game stream for real-time updates
	if err := pc.StartGameStream(ctx); err != nil {
		pc.log.Warnf("Failed to start game stream: %v", err)
		// Don't return error here since table creation was successful
	}

	return resp.TableId, nil
}

// JoinTable joins an existing poker table and tracks the table ID
func (pc *PokerClient) JoinTable(ctx context.Context, tableID string) error {
	resp, err := pc.LobbyService.JoinTable(ctx, &pokerrpc.JoinTableRequest{
		PlayerId: pc.ID,
		TableId:  tableID,
	})
	if err != nil {
		return err
	}

	if !resp.Success {
		return fmt.Errorf("failed to join table: %s", resp.Message)
	}

	pc.tableID = tableID

	// Start game stream for real-time updates
	if err := pc.StartGameStream(ctx); err != nil {
		pc.log.Warnf("Failed to start game stream: %v", err)
		// Don't return error here since joining was successful
	}

	return nil
}

// LeaveTable leaves the current table and clears the table ID
func (pc *PokerClient) LeaveTable(ctx context.Context) error {
	pc.RLock()
	tableID := pc.tableID
	pc.RUnlock()

	if tableID == "" {
		return fmt.Errorf("not currently in a table")
	}

	// Stop game stream first
	pc.stopGameStream()

	resp, err := pc.LobbyService.LeaveTable(ctx, &pokerrpc.LeaveTableRequest{
		PlayerId: pc.ID,
		TableId:  tableID,
	})
	if err != nil {
		return err
	}

	if !resp.Success {
		return fmt.Errorf("failed to leave table: %s", resp.Message)
	}

	pc.Lock()
	pc.tableID = ""
	pc.Unlock()

	return nil
}

// GetTables returns all available tables
func (pc *PokerClient) GetTables(ctx context.Context) ([]*pokerrpc.Table, error) {
	resp, err := pc.LobbyService.GetTables(ctx, &pokerrpc.GetTablesRequest{})
	if err != nil {
		return nil, err
	}
	return resp.Tables, nil
}

// GetPlayerCurrentTable returns the current table for the player
func (pc *PokerClient) GetPlayerCurrentTable(ctx context.Context) (string, error) {
	resp, err := pc.LobbyService.GetPlayerCurrentTable(ctx, &pokerrpc.GetPlayerCurrentTableRequest{
		PlayerId: pc.ID,
	})
	if err != nil {
		return "", err
	}
	return resp.TableId, nil
}

// GetBalance returns the current balance for the player
func (pc *PokerClient) GetBalance(ctx context.Context) (int64, error) {
	resp, err := pc.LobbyService.GetBalance(ctx, &pokerrpc.GetBalanceRequest{
		PlayerId: pc.ID,
	})
	if err != nil {
		return 0, err
	}
	return resp.Balance, nil
}

// UpdateBalance updates the player's balance
func (pc *PokerClient) UpdateBalance(ctx context.Context, amount int64, description string) (int64, error) {
	resp, err := pc.LobbyService.UpdateBalance(ctx, &pokerrpc.UpdateBalanceRequest{
		PlayerId:    pc.ID,
		Amount:      amount,
		Description: description,
	})
	if err != nil {
		return 0, err
	}
	return resp.NewBalance, nil
}

// ProcessTip processes a tip from this player to another player
func (pc *PokerClient) ProcessTip(ctx context.Context, toPlayerID string, amount int64, message string) (int64, error) {
	resp, err := pc.LobbyService.ProcessTip(ctx, &pokerrpc.ProcessTipRequest{
		FromPlayerId: pc.ID,
		ToPlayerId:   toPlayerID,
		Amount:       amount,
		Message:      message,
	})
	if err != nil {
		return 0, err
	}

	if !resp.Success {
		return 0, fmt.Errorf("failed to process tip: %s", resp.Message)
	}

	return resp.NewBalance, nil
}

// SetPlayerReady sets the player ready status
func (pc *PokerClient) SetPlayerReady(ctx context.Context) error {
	pc.RLock()
	tableID := pc.tableID
	pc.RUnlock()

	if tableID == "" {
		return fmt.Errorf("not currently in a table")
	}

	resp, err := pc.LobbyService.SetPlayerReady(ctx, &pokerrpc.SetPlayerReadyRequest{
		PlayerId: pc.ID,
		TableId:  tableID,
	})
	if err != nil {
		return err
	}

	if !resp.Success {
		return fmt.Errorf("failed to set ready: %s", resp.Message)
	}

	return nil
}

// SetPlayerUnready sets the player unready status
func (pc *PokerClient) SetPlayerUnready(ctx context.Context) error {
	pc.RLock()
	tableID := pc.tableID
	pc.RUnlock()

	if tableID == "" {
		return fmt.Errorf("not currently in a table")
	}

	resp, err := pc.LobbyService.SetPlayerUnready(ctx, &pokerrpc.SetPlayerUnreadyRequest{
		PlayerId: pc.ID,
		TableId:  tableID,
	})
	if err != nil {
		return err
	}

	if !resp.Success {
		return fmt.Errorf("failed to set unready: %s", resp.Message)
	}

	return nil
}

// StartNotifier starts the notification stream to receive server notifications
func (pc *PokerClient) StartNotificationStream(ctx context.Context) error {
	// Validate that client is properly initialized
	if err := pc.validate(); err != nil {
		return fmt.Errorf("cannot start notifier: %v", err)
	}

	// Create notification stream
	notificationStream, err := pc.LobbyService.StartNotificationStream(ctx, &pokerrpc.StartNotificationStreamRequest{
		PlayerId: pc.ID,
	})
	if err != nil {
		return fmt.Errorf("error creating notification stream: %w", err)
	}
	pc.notifier = notificationStream

	go func() {
		for {
			select {
			case <-ctx.Done():
				pc.log.Info("notification stream closed")
				return
			default:
				ntfn, err := pc.notifier.Recv()
				if err != nil {
					if errors.Is(err, io.EOF) || strings.Contains(err.Error(), "transport is closing") ||
						strings.Contains(err.Error(), "connection is being forcefully terminated") {

						// Try to reconnect
						reconnectErr := pc.reconnect()
						if reconnectErr != nil {
							pc.ErrorsCh <- fmt.Errorf("failed to reconnect: %v", reconnectErr)
						}
						return // This goroutine ends, but a new one will be started by reconnect()
					}

					pc.ErrorsCh <- fmt.Errorf("notification stream error: %v", err)
					return
				}

				// Check if notification is nil
				if ntfn == nil {
					pc.log.Debug("received nil notification")
					continue
				}

				// Check if notification manager is initialized
				if pc.ntfns == nil {
					pc.log.Error("notification manager is nil, skipping notification handling")
					continue
				}

				// Handle notifications based on NotificationType
				ts := time.Now()
				switch ntfn.Type {
				case pokerrpc.NotificationType_TABLE_CREATED:
					if ntfn.Table != nil {
						pc.ntfns.notifyTableCreated(ntfn.Table, ts)
					}

				case pokerrpc.NotificationType_PLAYER_JOINED:
					if ntfn.Table != nil {
						pc.ntfns.notifyPlayerJoined(ntfn.Table, ntfn.PlayerId, ts)
					}

				case pokerrpc.NotificationType_PLAYER_LEFT:
					if ntfn.Table != nil {
						pc.ntfns.notifyPlayerLeft(ntfn.Table, ntfn.PlayerId, ts)
					}

				case pokerrpc.NotificationType_GAME_STARTED:
					if ntfn.Started {
						pc.ntfns.notifyGameStarted(ntfn.TableId, ts)
					}

				case pokerrpc.NotificationType_GAME_ENDED:
					pc.ntfns.notifyGameEnded(ntfn.TableId, ntfn.Message, ts)
					pc.log.Info(ntfn.Message)

				case pokerrpc.NotificationType_BET_MADE:
					pc.ntfns.notifyBetMade(ntfn.PlayerId, ntfn.Amount, ts)
					if ntfn.PlayerId == pc.ID {
						pc.Lock()
						pc.BetAmt = ntfn.Amount
						pc.Unlock()
					}

					// Check the message content to determine specific action type
					if strings.Contains(ntfn.Message, "called") {
						pc.ntfns.notifyPlayerCalled(ntfn.PlayerId, ntfn.Amount, ts)
					} else if strings.Contains(ntfn.Message, "raised") {
						pc.ntfns.notifyPlayerRaised(ntfn.PlayerId, ntfn.Amount, ts)
					} else if strings.Contains(ntfn.Message, "checked") {
						pc.ntfns.notifyPlayerChecked(ntfn.PlayerId, ts)
					}

				case pokerrpc.NotificationType_PLAYER_FOLDED:
					pc.ntfns.notifyPlayerFolded(ntfn.PlayerId, ts)

				case pokerrpc.NotificationType_PLAYER_READY:
					pc.ntfns.notifyPlayerReady(ntfn.PlayerId, ntfn.Ready, ts)
					if ntfn.PlayerId == pc.ID {
						pc.Lock()
						pc.IsReady = ntfn.Ready
						pc.Unlock()
					}
					// Forward notification to UI for any player ready event
					pc.UpdatesCh <- ntfn

				case pokerrpc.NotificationType_PLAYER_UNREADY:
					pc.ntfns.notifyPlayerReady(ntfn.PlayerId, false, ts)
					if ntfn.PlayerId == pc.ID {
						pc.Lock()
						pc.IsReady = false
						pc.Unlock()
					}
					// Forward notification to UI
					pc.UpdatesCh <- ntfn

				case pokerrpc.NotificationType_ALL_PLAYERS_READY:
					// Forward game ready to play notifications to UI
					pc.UpdatesCh <- ntfn

				case pokerrpc.NotificationType_BALANCE_UPDATED:
					pc.ntfns.notifyBalanceUpdated(ntfn.PlayerId, ntfn.NewBalance, ts)

				case pokerrpc.NotificationType_TIP_RECEIVED:
					// Extract tip details from notification
					fromID := ntfn.PlayerId // Assuming the sender is in PlayerId field
					toID := pc.ID           // For now, assume tip is to this client
					amount := ntfn.Amount
					message := ntfn.Message
					pc.ntfns.notifyTipReceived(fromID, toID, amount, message, ts)

				case pokerrpc.NotificationType_SHOWDOWN_RESULT:
					pc.ntfns.notifyShowdownResult(ntfn.TableId, ntfn.Winners, ts)

				case pokerrpc.NotificationType_NEW_ROUND:
					// Forward to UI
					pc.UpdatesCh <- ntfn

				case pokerrpc.NotificationType_SMALL_BLIND_POSTED:
					if pc.ntfns != nil {
						pc.ntfns.notifyBetMade(ntfn.PlayerId, ntfn.Amount, ts)
					}
					pc.log.Infof("Small blind posted: %d chips by %s", ntfn.Amount, ntfn.PlayerId)

				case pokerrpc.NotificationType_BIG_BLIND_POSTED:
					if pc.ntfns != nil {
						pc.ntfns.notifyBetMade(ntfn.PlayerId, ntfn.Amount, ts)
					}
					pc.log.Infof("Big blind posted: %d chips by %s", ntfn.Amount, ntfn.PlayerId)

				default:
					pc.log.Debug("received unknown notification type", "type", ntfn.Type)
				}

				// Always forward raw notification to updates channel for UI handling
				pc.UpdatesCh <- ntfn
			}
		}
	}()

	return nil
}
