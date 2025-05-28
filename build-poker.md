# Poker Bot Development Prompt for Bison Relay

This document provides a structured outline for creating a Poker Bot leveraging the Bison Relay network, inspired by the Pong game project and the BisonBotKit.

## Project Overview

Create a Poker Bot responsible for managing poker games within the Bison Relay network. The bot will:

- ✅ Manage poker tables and game state.
- ⏳ Evaluate poker hands.
- ⏳ Distribute pots and finalize games.
- ✅ Track and update player balances based on received tips.
- ✅ Allow players to create or join poker tables by spending their balance.

## Key Features

### 1. Tip Management
- ✅ Users send tips directly to the bot.
- ✅ Received tips update a SQLite database tracking each user's balance.
- ✅ Provide feedback or confirmation of balance updates.

### 2. Table Management
- ⏳ Players spend their balance to create or join poker tables (only spend after the game occuring).
- ✅ Tables have configurable requirements for minimum balance and the required number of players to start (e.g., Sit 'n Go format).
- ✅ Automatically start games when the required number of players join, check if player has enough balance before joining table.

### 3. Game State Management
- ✅ Implement a state machine (`stateFn`) to manage game phases:
  - ✅ Waiting for players.
  - ⏳ Pre-flop, Flop, Turn, and River betting rounds.
  - ⏳ Showdown and hand evaluation.
  - ⏳ Pot distribution.

### 4. Hand Evaluation
- ⏳ Integrate poker hand evaluation logic to determine winners accurately.
- ⏳ Compare hands according to standard poker rules.
- ⏳ For reference: https://github.com/chehsunliu/poker

### 5. Pot Management
- ⏳ Accurately track bets placed by players.
- ⏳ Distribute pot winnings to the appropriate players' balances upon completion of each game.

### 6. Notifications
- ✅ Inform players about game status, their current balances, and table activities through the Bison Relay messaging system.
- ⏳ Send real-time updates about game actions and state changes.

## Technical Requirements
- ✅ Utilize GRPC for managing communication between server and clients.
- ✅ Utilize SQLite for persisting game states and player balances.
- ✅ Use Go for backend logic with BisonBotKit for interactions within the Bison Relay network.
- ✅ Leverage existing patterns and architectures from your Pong game for robust state management and network communications.

## Development Steps

1. ✅ Set up the basic project structure, referencing the Pong project and BisonBotKit.
2. ✅ Implement user balance management and tip handling.
3. ✅ Develop table creation and joining mechanisms.
4. ⏳ Create the poker game state machine.
5. ⏳ Add hand evaluation and pot distribution logic.
6. ✅ Integrate notifications via Bison Relay messaging.
7. ⏳ Test each component thoroughly and ensure robustness.

## Outcome
A reliable, decentralized poker bot enhancing the Bison Relay ecosystem by providing engaging and fair poker games for users.

## Current Progress Status

### Completed Features
1. ✅ **Basic Lobby System**
   - Table creation with configurable parameters
   - Table joining and listing
   - Player readiness management
   - Automatic game start when all players are ready

2. ✅ **Client UI**
   - Terminal-based UI for interacting with the poker server
   - Menu navigation for all lobby operations
   - Real-time polling for game state updates
   - Game view with player status and table information

3. ✅ **Balance Management**
   - Tracking player balances in SQLite
   - Checking balance before joining tables
   - Processing tips between players

4. ✅ **GRPC Communication**
   - Server-client architecture for poker operations
   - API endpoints for all lobby functions

### What's Still Missing For A Production-Ready Release

The prototype has a working lobby system, but several critical areas remain before it can be safely deployed in the wild:

1. **Hand Evaluation & Showdown Logic** ⏳  
   • Integrate a proven evaluator (e.g. chehsunliu/poker) to score all 7-card combinations.  
   • Support ties, side-pots and split-pot payouts.  
   • Replace the current stub that simply awards the pot to the first active player.

2. **Betting Engine & Pot Accounting** ⏳  
   • Enforce blinds/antes, minimum-raise rules, all-in handling and creation of side pots.  
   • Track per-street pots and chip commitments so that the "uncalled bet" is automatically returned.
   • Implement proper balance deduction when joining tables and making bets.

3. **Dealer & Blind Rotation** ⏳  
   • At the start of each new hand rotate dealer, small blind and big blind positions and auto-post the blinds.

4. **Real-Time Game Streaming** ⏳  
   • Replace polling with proper subscription-based updates.
   • Expand `Table.Subscribe` and gRPC `StartGameStream` / `StartNotificationStream` to push every state change: seat updates, bets, folds, community cards, showdowns and balance changes.  
   • Include per-player private messages for hole cards.

5. **Persistence & Recovery** ⏳  
   • Persist open tables, seats and hands so the service can crash/restart without losing money or game context.  
   • Use SQLite migrations or a lightweight event-sourcing log.

6. **Server Entrypoint & Ops** ⏳  
   • Add `cmd/server/main.go` that wires the gRPC server, loads config/DB and exposes health probes.  
   • Provide Dockerfile/Compose and CI pipeline.

7. **Security / Fair-Shuffle** ⏳  
   • Decide on trust model: central dealer vs. verifiable shuffle.  
   • If decentralised, implement commit-reveal or mental-poker protocol so players can audit the RNG.  
   • Enable TLS & auth for gRPC endpoints.

8. **Player Time-Bank & Auto-Action** ⏳  
   • Auto-check/fold/call when a player's timebank expires instead of only folding.  
   • Expose remaining time to clients.

9. **Improved Game UI** ⏳  
   • Enhance the game screen with better card visualization.
   • Add chip/pot representation and betting interface.
   • Better indication of action sequence.
   • Relay game updates through Bison PMs for users that stay inside chat only.

10. **Comprehensive Testing & Auditing** ⏳  
    • Add integration tests that simulate multiple seats through a full tournament.  
    • Lint, vet, static-analysis and memory leak tests in CI.

Addressing the above items elevates the prototype to a secure, fault-tolerant, production-ready poker service on Bison Relay.

