# Pong on Bison Relay - gRPC API Documentation

This document outlines the gRPC API endpoints for the Pong game.

## API Endpoints

### Game Play
- **SendInput**: Sends player input commands (up/down)
  - Request: `PlayerInput` with player ID and input direction
  - Response: `GameUpdate` with updated game state

- **StartGameStream**: Opens a stream to receive real-time game state updates
  - Request: `StartGameStreamRequest` with client ID
  - Response: Stream of `GameUpdateBytes` containing serialized game state

- **UnreadyGameStream**: Mark player as not ready
  - Request: `UnreadyGameStreamRequest` with client ID
  - Response: `UnreadyGameStreamResponse`

### Notifications
- **StartNtfnStream**: Opens a stream to receive game notifications
  - Request: `StartNtfnStreamRequest` with client ID
  - Response: Stream of `NtfnStreamResponse` with various notification types

### Waiting Room Management
- **GetWaitingRoom**: Get details for a specific waiting room
  - Request: `WaitingRoomRequest` 
  - Response: `WaitingRoomResponse` with list of players

- **GetWaitingRooms**: List all available waiting rooms
  - Request: `WaitingRoomsRequest` (optionally with room ID)
  - Response: `WaitingRoomsResponse` with array of waiting rooms

- **CreateWaitingRoom**: Create a new waiting room
  - Request: `CreateWaitingRoomRequest` with host ID and bet amount
  - Response: `CreateWaitingRoomResponse` with waiting room details

- **JoinWaitingRoom**: Join an existing waiting room
  - Request: `JoinWaitingRoomRequest` with room and client IDs
  - Response: `JoinWaitingRoomResponse` with waiting room details

- **LeaveWaitingRoom**: Leave a waiting room
  - Request: `LeaveWaitingRoomRequest` with client and room IDs
  - Response: `LeaveWaitingRoomResponse` with success status

## Notification Types

The API uses the following notification types:

- `UNKNOWN`: Default unknown notification
- `MESSAGE`: Generic message notification
- `GAME_START`: Game has started
- `GAME_END`: Game has ended
- `OPPONENT_DISCONNECTED`: Opponent has left the game
- `BET_AMOUNT_UPDATE`: Bet amount has been updated
- `PLAYER_JOINED_WR`: Player joined waiting room
- `ON_WR_CREATED`: Waiting room was created
- `ON_PLAYER_READY`: Player is ready
- `ON_WR_REMOVED`: Waiting room was removed
- `PLAYER_LEFT_WR`: Player left waiting room

## Data Models

### Game State
- `GameUpdate`: Contains full game state including:
  - Ball position and velocity
  - Paddle positions and velocities
  - Game dimensions
  - Player scores
  - Performance metrics (FPS/TPS)

### Player Data
- `Player`: Contains player information:
  - Unique ID
  - Nickname
  - Bet amount
  - Player number (1 or 2)
  - Score
  - Ready status

### Waiting Room
- `WaitingRoom`: Contains waiting room details:
  - Room ID
  - Host ID
  - List of players
  - Bet amount
