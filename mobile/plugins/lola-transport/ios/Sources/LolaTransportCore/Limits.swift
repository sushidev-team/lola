import Foundation

/// The numeric limits of 04 §1.2 and the timing table of §1.4. TypeScript
/// mirrors the ones JS needs in `definitions.ts` / `wire/limits.ts`.
public enum Limits {
    public static let maxFrameBytes = LolaFrameLimits.maxFrameBytes
    public static let defaultPort = 7717

    /// Longest scanned pairing string; longer is `malformed`.
    public static let maxPairPayloadChars = 1024
    /// A scanned payload is dropped from memory after this.
    public static let pairHoldMs = 300_000
    public static let maxPickLongEdgePx = 2048
    public static let maxPickBytes = 4 << 20
    public static let maxClipboardBytes = 8 << 20
    /// Inbound coalescing window.
    public static let flushIntervalMs = 16
    /// Flush early above this many buffered bytes.
    public static let maxBatchBytes = 262_144
    public static let hapticMinGapMs = 50
    /// `adopt` is taken by the plugin itself after this long.
    public static let migrateDrainMs = 2000
    public static let pathDebounceMs = 500
    /// An inbound loopback connection that is not the inner connection's own.
    public static let loopbackMatchTimeoutMs = 2000
}

/// 04 §1.4 schedule. Millisecond values on a monotonic clock.
public enum RaceTiming {
    public static let directStaggerMs = 150
    public static let relayDelayMs = 300
    public static let directAttemptTimeoutMs = 4000
    public static let relayUpgradeTimeoutMs = 8000
    public static let relayAttemptTimeoutMs = 12_000
    public static let raceTimeoutMs = 13_000
    public static let preferDirectGraceMs = 250
    public static let maxParallelAttempts = 3
    /// The daemon refuses a third pre-auth socket from one address.
    public static let maxDirectParallel = 2
    public static let maxMigrationAttempts = 2
    public static let maxMdnsCandidates = 8
    /// A pairing race holds `maxUnpairedPerSource` slots at most.
    public static let pairMaxParallel = 2
}
