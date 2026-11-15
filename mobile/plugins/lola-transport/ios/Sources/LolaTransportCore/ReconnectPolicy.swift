import Foundation

/// The two backoff ladders of 04 §1.6 and §1.3.
///
/// - Connect ladder (whole-connection retry): 0, 1, 2, 4, 8, 15 s, then 15 s.
///   Reset on a path change, on foreground and on `connect()`.
/// - Relay ladder (relay candidate only): 2, 4, 8, 16, 32, 60 s, then 60 s.
///   Direct attempts never follow it.
///
/// A server-sent floor (`Retry-After`) lifts a delay and never lowers it.
public struct ReconnectPolicy: Equatable, Sendable {
    public static let connectLadderMs = [0, 1000, 2000, 4000, 8000, 15_000]
    public static let relayLadderMs = [2000, 4000, 8000, 16_000, 32_000, 60_000]

    private var connectStep = 0
    private var relayStep = 0

    public init() {}

    /// The delay before the next connection attempt; advances the ladder.
    public mutating func nextConnectDelayMs(floorMs: Int = 0) -> Int {
        let delay = Self.rung(Self.connectLadderMs, connectStep)
        connectStep += 1
        return max(delay, floorMs)
    }

    /// The delay before the next relay attempt; advances the ladder.
    public mutating func nextRelayDelayMs(floorMs: Int = 0) -> Int {
        let delay = Self.rung(Self.relayLadderMs, relayStep)
        relayStep += 1
        return max(delay, floorMs)
    }

    public mutating func resetConnect() { connectStep = 0 }
    public mutating func resetRelay() { relayStep = 0 }
    public mutating func reset() { connectStep = 0; relayStep = 0 }

    /// The rung of the relay ladder after `failures` consecutive failures
    /// (1 = first failure), for callers that persist the count instead.
    public static func relayDelayMs(failures: Int) -> Int {
        rung(relayLadderMs, max(0, failures - 1))
    }

    private static func rung(_ ladder: [Int], _ step: Int) -> Int {
        ladder[min(step, ladder.count - 1)]
    }
}
