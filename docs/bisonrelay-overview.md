# BisonRelay: The Sovereign Internet Platform

## Overview

**BisonRelay** is a revolutionary privacy-focused communications platform built by Decred that serves as a complete alternative to traditional web infrastructure. It enables free speech, free association, and sovereign communications through a decentralized, censorship-resistant architecture.

## What Makes BisonRelay Unique

### No Accounts, No Surveillance
Unlike traditional platforms, BisonRelay eliminates user accounts entirely. Every message is handled individually and paid for via Lightning Network micropayments, removing the infrastructure typically used for surveillance and censorship.

### Privacy by Design
- **End-to-end encryption** using Double Ratchet + post-quantum cryptography
- **Metadata minimization** - servers cannot see sender, receiver, or message content
- **Deniable messaging** - provides plausible deniability for communications
- **Forward secrecy** - temporary compromise doesn't affect past/future messages

### Integrated Payments
Every message costs approximately 1 atom (0.00000001 DCR) per kilobyte, creating:
- **Anti-spam protection** through economic disincentives
- **Self-sustaining infrastructure** without advertising or data monetization
- **Global accessibility** with minimal Decred requirements (0.1 DCR lasts months)

## Architecture

### Core Components

#### Client-Server Protocol
- **Asynchronous messaging** with individual message handling
- **30-day encrypted storage** before automatic purging
- **Lightning Network micropayments** required for all operations

#### Peer-to-Peer Networking
- **Mediated key exchanges** for connecting new users through mutual contacts
- **Relay system** for amplifying content without central servers
- **Invitation-based onboarding** via out-of-band secure channels

#### Social Media Functionality
- **Decentralized posts** and subscriptions
- **Comment threading** and interactions
- **Content relaying** similar to traditional social platforms
- **Real-time notifications** system

## Technical Implementation

### Technology Stack
- **Backend**: Go-based implementation
- **Transport**: gRPC over encrypted channels
- **Payments**: Decred Lightning Network integration
- **UI Options**: Terminal (BubbleTea) and Flutter interfaces

### Key Features
- **Cross-platform clients** (Windows, macOS, Linux, Android)
- **CLI and GUI interfaces** for different user preferences
- **Bot automation** support via client RPC
- **Simple store** for digital goods and services

## Use Cases

### Communications
- Private messaging without surveillance
- Group chats with strong privacy guarantees
- File sharing with built-in payments
- Voice/video calls (future development)

### Social Networking
- Decentralized social media without censorship
- Content creation and monetization
- Community building without platform risk
- Cross-platform content distribution

### Commerce
- Digital goods marketplace
- Subscription services
- Tipping and donations
- Micro-transaction enabled applications

## Getting Started

### Requirements
- Small amount of Decred (0.1 DCR recommended)
- BisonRelay client (GUI or CLI)
- Lightning Network channels with sufficient liquidity

### Basic Setup
1. Download and install BisonRelay client
2. Fund Lightning Network wallet
3. Open channels with sufficient outbound capacity
4. Generate or accept invitations to connect with others

### Configuration
```ini
[clientrpc]
jsonrpclisten = 127.0.0.1:7676
rpccertpath = ~/.brclient/rpc.cert
rpckeypath = ~/.brclient/rpc.key
rpcuser = your_username
rpcpass = your_password
```

## Security Model

### Threat Assumptions
- Malicious server operators
- Powerful adversaries controlling infrastructure
- Quantum computing attacks
- Metadata analysis attempts

### Protections
- **Double Ratchet encryption** with forward/reverse secrecy
- **Post-quantum secure PKI** against future quantum attacks
- **Deniable messaging** preventing message attribution
- **Minimal metadata** preventing traffic analysis

## Economic Model

### Pricing Structure
- **1 atom per KB** for sent data (storage cost)
- **1 atom per message** for received data (processing cost)
- **30-day retention** before automatic deletion
- **No subscription fees** - pay-as-you-go model

### Sustainability
- **Self-funding** through usage fees
- **No advertising** or data monetization
- **Global accessibility** with minimal barriers
- **Scalable infrastructure** through Lightning Network

## Development & Community

### Open Source
- **ISC License** - permissive open source
- **GitHub Repository**: companyzero/bisonrelay
- **Active development** with regular releases
- **Community contributions** welcome

### Resources
- **Official Website**: bisonrelay.org
- **Documentation**: Comprehensive guides and tutorials
- **Community Chat**: Active development discussions
- **Bug Reports**: GitHub issues system

## Future Roadmap

### Planned Features
- **Enhanced mobile support** with improved UX
- **Voice/video integration** using WebRTC
- **Advanced commerce tools** for digital marketplaces
- **Cross-platform synchronization** improvements

### Research Areas
- **Quantum-resistant cryptography** upgrades
- **Scalability enhancements** for mass adoption
- **Interoperability** with other privacy platforms
- **Decentralized governance** mechanisms

## Conclusion

BisonRelay represents a paradigm shift in how we think about online communications. By combining privacy-by-design principles with economic incentives through the Lightning Network, it creates a sustainable, censorship-resistant platform that puts users in complete control of their digital communications.

Whether you're building applications like poker games, creating private communities, or simply seeking sovereign communications, BisonRelay provides the infrastructure for a truly decentralized internet.

---
