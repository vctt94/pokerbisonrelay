// Example usage of PokerGame component
import 'package:flutter/material.dart';
import 'package:pokerui/models/poker.dart';
import 'package:pokerui/components/poker_game.dart';

class PokerGameExample extends StatefulWidget {
  final PokerModel pokerModel;
  
  const PokerGameExample({
    super.key,
    required this.pokerModel,
  });

  @override
  State<PokerGameExample> createState() => _PokerGameExampleState();
}

class _PokerGameExampleState extends State<PokerGameExample> {
  late FocusNode _focusNode;
  late PokerGame _pokerGame;

  @override
  void initState() {
    super.initState();
    _focusNode = FocusNode();
    _pokerGame = PokerGame(widget.pokerModel.playerId, widget.pokerModel);
  }

  @override
  void dispose() {
    _focusNode.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final game = widget.pokerModel.game;
    if (game == null) {
      return const Center(
        child: Text(
          'No game in progress',
          style: TextStyle(color: Colors.white),
        ),
      );
    }

    return Scaffold(
      backgroundColor: Colors.black,
      body: Stack(
        children: [
          // Main poker game visualization
          _pokerGame.buildWidget(game, _focusNode),
          
          // Ready to play overlay (if needed)
          if (!game.gameStarted)
            _pokerGame.buildReadyToPlayOverlay(
              context,
              false, // isReadyToPlay
              false, // countdownStarted
              'Get Ready!', // countdownMessage
              () {
                // Handle ready button press
                widget.pokerModel.setReady();
              },
              game,
            ),
          
          // Action buttons overlay
          Positioned(
            bottom: 20,
            left: 0,
            right: 0,
            child: _buildActionButtons(),
          ),
        ],
      ),
    );
  }

  Widget _buildActionButtons() {
    return Container(
      padding: const EdgeInsets.all(16),
      child: Row(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          if (widget.pokerModel.isMyTurn) ...[
            _buildActionButton(
              'Fold',
              Colors.red,
              () => widget.pokerModel.fold(),
            ),
            const SizedBox(width: 8),
            _buildActionButton(
              'Check',
              Colors.blue,
              () => widget.pokerModel.check(),
            ),
            const SizedBox(width: 8),
            _buildActionButton(
              'Call',
              Colors.orange,
              () => widget.pokerModel.callBet(),
            ),
            const SizedBox(width: 8),
            _buildActionButton(
              'Bet',
              Colors.green,
              () => widget.pokerModel.makeBet(100),
            ),
          ] else ...[
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 20, vertical: 10),
              decoration: BoxDecoration(
                color: Colors.black.withOpacity(0.7),
                borderRadius: BorderRadius.circular(20),
              ),
              child: const Text(
                'Waiting for your turn...',
                style: TextStyle(color: Colors.white, fontSize: 16),
              ),
            ),
          ],
        ],
      ),
    );
  }

  Widget _buildActionButton(String label, Color color, VoidCallback onPressed) {
    return ElevatedButton(
      onPressed: onPressed,
      style: ElevatedButton.styleFrom(
        backgroundColor: color,
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(20),
        ),
      ),
      child: Text(
        label,
        style: const TextStyle(
          color: Colors.white,
          fontWeight: FontWeight.bold,
        ),
      ),
    );
  }
}

// Keyboard shortcuts example
class PokerGameWithKeyboard extends StatefulWidget {
  final PokerModel pokerModel;
  
  const PokerGameWithKeyboard({
    super.key,
    required this.pokerModel,
  });

  @override
  State<PokerGameWithKeyboard> createState() => _PokerGameWithKeyboardState();
}

class _PokerGameWithKeyboardState extends State<PokerGameWithKeyboard> {
  late FocusNode _focusNode;
  late PokerGame _pokerGame;

  @override
  void initState() {
    super.initState();
    _focusNode = FocusNode();
    _pokerGame = PokerGame(widget.pokerModel.playerId, widget.pokerModel);
  }

  @override
  void dispose() {
    _focusNode.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final game = widget.pokerModel.game;
    if (game == null) {
      return const Center(
        child: Text(
          'No game in progress',
          style: TextStyle(color: Colors.white),
        ),
      );
    }

    return Scaffold(
      backgroundColor: Colors.black,
      body: Focus(
        autofocus: true,
        child: _pokerGame.buildWidget(
          game, 
          _focusNode,
          onReadyHotkey: () {
            // Space or R key pressed - ready up
            widget.pokerModel.setReady();
          },
        ),
      ),
    );
  }
}
