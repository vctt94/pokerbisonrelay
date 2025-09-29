import 'package:flutter/material.dart';
import 'package:pokerui/models/poker.dart';
import 'package:pokerui/components/poker_game.dart';

/// Poker main content widget that displays tables and game state
class PokerMainContent extends StatefulWidget {
  final PokerModel model;
  const PokerMainContent({super.key, required this.model});

  @override
  State<PokerMainContent> createState() => _PokerMainContentState();
}

class _PokerMainContentState extends State<PokerMainContent> {
  @override
  Widget build(BuildContext context) {
    // Show appropriate content based on current state
    switch (widget.model.state) {
      case PokerState.idle:
        return _buildIdleState(context, widget.model);
      case PokerState.browsingTables:
        return _buildBrowsingTablesState(context, widget.model);
      case PokerState.inLobby:
        return _buildInLobbyState(context, widget.model);
      case PokerState.handInProgress:
        return _buildHandInProgressState(context, widget.model);
      case PokerState.showdown:
        return _buildShowdownState(context, widget.model);
      case PokerState.tournamentOver:
        return _buildTournamentOverState(context, widget.model);
    }
  }

  Widget _buildIdleState(BuildContext context, PokerModel model) {
    return Center(
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          const Icon(Icons.casino, size: 64, color: Colors.white70),
          const SizedBox(height: 16),
          const Text(
            'Welcome to Poker!',
            style: TextStyle(fontSize: 24, fontWeight: FontWeight.bold, color: Colors.white),
          ),
          const SizedBox(height: 8),
          const Text(
            'Connect to a poker server to start playing',
            style: TextStyle(color: Colors.white70),
          ),
          const SizedBox(height: 24),
          Row(
            mainAxisAlignment: MainAxisAlignment.center,
            children: [
              ElevatedButton.icon(
                onPressed: () {
                  model.refreshTables();
                },
                icon: const Icon(Icons.refresh),
                label: const Text('Connect & Refresh'),
                style: ElevatedButton.styleFrom(backgroundColor: Colors.blue),
              ),
              const SizedBox(width: 16),
              ElevatedButton.icon(
                onPressed: () {
                  // TODO: Implement create table functionality
                  ScaffoldMessenger.of(context).showSnackBar(
                    const SnackBar(content: Text('Create table functionality coming soon')),
                  );
                },
                icon: const Icon(Icons.add),
                label: const Text('Create Table'),
                style: ElevatedButton.styleFrom(backgroundColor: Colors.green),
              ),
            ],
          ),
        ],
      ),
    );
  }

  Widget _buildBrowsingTablesState(BuildContext context, PokerModel model) {
    if (model.tables.isEmpty) {
      return Center(
        child: Column(
          mainAxisAlignment: MainAxisAlignment.center,
          children: [
            const Icon(Icons.table_restaurant, size: 64, color: Colors.white70),
            const SizedBox(height: 16),
            const Text(
              'No Tables Available',
              style: TextStyle(fontSize: 20, fontWeight: FontWeight.bold, color: Colors.white),
            ),
            const SizedBox(height: 8),
            const Text(
              'Create a new table to start playing',
              style: TextStyle(color: Colors.white70),
            ),
            const SizedBox(height: 24),
            ElevatedButton.icon(
              onPressed: () {
                // TODO: Implement create table functionality
                ScaffoldMessenger.of(context).showSnackBar(
                  const SnackBar(content: Text('Create table functionality coming soon')),
                );
              },
              icon: const Icon(Icons.add),
              label: const Text('Create Table'),
              style: ElevatedButton.styleFrom(backgroundColor: Colors.blue),
            ),
          ],
        ),
      );
    }

    return RefreshIndicator(
      onRefresh: () => model.refreshTables(),
      child: ListView.builder(
        padding: const EdgeInsets.all(16),
        shrinkWrap: true,
        physics: const AlwaysScrollableScrollPhysics(),
        itemCount: model.tables.length,
        itemBuilder: (context, index) {
          final table = model.tables[index];
          return Card(
            margin: const EdgeInsets.only(bottom: 12),
            color: const Color(0xFF1B1E2C),
            shape: RoundedRectangleBorder(
              borderRadius: BorderRadius.circular(12),
            ),
            child: Padding(
              padding: const EdgeInsets.all(16),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Row(
                    children: [
                      const Icon(Icons.table_restaurant, color: Colors.blue, size: 24),
                      const SizedBox(width: 8),
                      Text(
                        'Table ${table.id.substring(0, 8)}...',
                        style: const TextStyle(
                          fontSize: 18,
                          fontWeight: FontWeight.bold,
                          color: Colors.white,
                        ),
                      ),
                      const Spacer(),
                      Container(
                        padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
                        decoration: BoxDecoration(
                          color: table.gameStarted ? Colors.green : Colors.orange,
                          borderRadius: BorderRadius.circular(12),
                        ),
                        child: Text(
                          table.gameStarted ? 'In Progress' : 'Waiting',
                          style: const TextStyle(
                            color: Colors.white,
                            fontSize: 12,
                            fontWeight: FontWeight.bold,
                          ),
                        ),
                      ),
                    ],
                  ),
                  const SizedBox(height: 12),
                  Row(
                    children: [
                      _buildInfoChip(Icons.people, '${table.currentPlayers}/${table.maxPlayers}'),
                      const SizedBox(width: 8),
                      _buildInfoChip(Icons.attach_money, '${table.smallBlind}/${table.bigBlind}'),
                      const SizedBox(width: 8),
                      _buildInfoChip(Icons.account_balance_wallet, '${(table.buyInAtoms / 1e8).toStringAsFixed(2)} DCR'),
                    ],
                  ),
                  const SizedBox(height: 12),
                  Row(
                    children: [
                      Expanded(
                        child: Text(
                          'Phase: ${table.phase.label}',
                          style: const TextStyle(color: Colors.white70),
                        ),
                      ),
                      ElevatedButton(
                        onPressed: () {
                          model.joinTable(table.id);
                        },
                        style: ElevatedButton.styleFrom(
                          backgroundColor: Colors.green,
                          foregroundColor: Colors.white,
                        ),
                        child: const Text('Join Table'),
                      ),
                    ],
                  ),
                ],
              ),
            ),
          );
        },
      ),
    );
  }

  Widget _buildInfoChip(IconData icon, String text) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
      decoration: BoxDecoration(
        color: Colors.grey.shade800,
        borderRadius: BorderRadius.circular(8),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(icon, size: 16, color: Colors.white70),
          const SizedBox(width: 4),
          Text(
            text,
            style: const TextStyle(color: Colors.white70, fontSize: 12),
          ),
        ],
      ),
    );
  }

  Widget _buildInLobbyState(BuildContext context, PokerModel model) {
    return Center(
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          const Icon(Icons.table_restaurant, size: 64, color: Colors.white70),
          const SizedBox(height: 16),
          Text(
            'Table: ${model.currentTableId}',
            style: const TextStyle(fontSize: 20, fontWeight: FontWeight.bold, color: Colors.white),
          ),
          const SizedBox(height: 8),
          Text(
            'State: ${model.state.name}',
            style: const TextStyle(color: Colors.white70),
          ),
          const SizedBox(height: 24),
          Row(
            mainAxisAlignment: MainAxisAlignment.center,
            children: [
              ElevatedButton(
                onPressed: model.iAmReady ? model.setUnready : model.setReady,
                child: Text(model.iAmReady ? 'Unready' : 'Ready'),
              ),
              const SizedBox(width: 16),
              ElevatedButton(
                onPressed: model.leaveTable,
                style: ElevatedButton.styleFrom(backgroundColor: Colors.red),
                child: const Text('Leave Table'),
              ),
            ],
          ),
        ],
      ),
    );
  }

  Widget _buildHandInProgressState(BuildContext context, PokerModel model) {
    final game = model.game;
    if (game == null) {
      return const Center(child: Text('No game data available'));
    }

    final focusNode = FocusNode();
    final pokerGame = PokerGame(model.playerId, model);

    return Stack(
      children: [
        // Poker game visualization
        pokerGame.buildWidget(game, focusNode),
        
        // Action buttons overlay
        Positioned(
          bottom: 20,
          left: 0,
          right: 0,
          child: Center(
            child: Row(
              mainAxisAlignment: MainAxisAlignment.center,
              children: [
                if (model.isMyTurn) ...[
                  ElevatedButton(
                    onPressed: () => model.fold(),
                    style: ElevatedButton.styleFrom(backgroundColor: Colors.red),
                    child: const Text('Fold (F)'),
                  ),
                  const SizedBox(width: 8),
                  ElevatedButton(
                    onPressed: () => model.check(),
                    child: const Text('Check (K)'),
                  ),
                  const SizedBox(width: 8),
                  ElevatedButton(
                    onPressed: () => model.callBet(),
                    child: const Text('Call (C)'),
                  ),
                  const SizedBox(width: 8),
                  ElevatedButton(
                    onPressed: () => model.makeBet(100), // Fixed bet for now
                    style: ElevatedButton.styleFrom(backgroundColor: Colors.green),
                    child: const Text('Bet (B)'),
                  ),
                ] else ...[
                  Container(
                    padding: const EdgeInsets.symmetric(horizontal: 20, vertical: 10),
                    decoration: BoxDecoration(
                      color: Colors.black.withOpacity(0.7),
                      borderRadius: BorderRadius.circular(20),
                    ),
                    child: Text(
                      'Waiting for your turn...',
                      style: const TextStyle(color: Colors.white, fontSize: 16),
                    ),
                  ),
                ],
              ],
            ),
          ),
        ),
      ],
    );
  }

  Widget _buildShowdownState(BuildContext context, PokerModel model) {
    return Center(
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          const Icon(Icons.emoji_events, size: 64, color: Colors.amber),
          const SizedBox(height: 16),
          const Text(
            'Showdown!',
            style: TextStyle(fontSize: 24, fontWeight: FontWeight.bold, color: Colors.white),
          ),
          const SizedBox(height: 16),
          if (model.lastWinners.isNotEmpty) ...[
            const Text('Winners:', style: TextStyle(color: Colors.white70)),
            const SizedBox(height: 8),
            ...model.lastWinners.map((winner) => Text(
              'Player ${winner.playerId}: ${winner.handRank.name}',
              style: const TextStyle(color: Colors.white70),
            )),
          ],
        ],
      ),
    );
  }

  Widget _buildTournamentOverState(BuildContext context, PokerModel model) {
    return Center(
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          const Icon(Icons.flag, size: 64, color: Colors.green),
          const SizedBox(height: 16),
          const Text(
            'Tournament Over!',
            style: TextStyle(fontSize: 24, fontWeight: FontWeight.bold, color: Colors.white),
          ),
          const SizedBox(height: 16),
          ElevatedButton(
            onPressed: () {
              model.leaveTable();
            },
            child: const Text('Return to Lobby'),
          ),
        ],
      ),
    );
  }

}