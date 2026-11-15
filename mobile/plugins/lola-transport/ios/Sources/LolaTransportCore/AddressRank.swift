import Foundation

/// Orders the daemon's addresses for a race (04 §1.4 item 2): private IPv4
/// first, other IPv4, IPv4 link-local, IPv6 (global and unique-local before
/// link-local), loopback last (kept for the Simulator). Stable within a tier,
/// deduplicated, and anything that is not an IP literal is dropped.
public enum AddressRank {
    public static func rank(_ addrs: [String]) -> [String] {
        var seen = Set<String>()
        let usable = addrs
            .map { $0.trimmingCharacters(in: .whitespaces) }
            .filter { !$0.isEmpty && !$0.contains("%") && tier(of: $0) != nil && seen.insert($0).inserted }
        return usable.enumerated()
            .sorted { (tier(of: $0.element)!, $0.offset) < (tier(of: $1.element)!, $1.offset) }
            .map(\.element)
    }

    /// Lower is tried first; `nil` is not an IP literal.
    static func tier(of host: String) -> Int? {
        var v4 = in_addr()
        if inet_pton(AF_INET, host, &v4) == 1 {
            let b = withUnsafeBytes(of: &v4) { Array($0) }
            if b[0] == 127 { return 5 }
            if b[0] == 10 || (b[0] == 172 && (16...31).contains(b[1])) || (b[0] == 192 && b[1] == 168) { return 0 }
            if b[0] == 169 && b[1] == 254 { return 2 }
            return 1
        }
        var v6 = in6_addr()
        if inet_pton(AF_INET6, host, &v6) == 1 {
            let b = withUnsafeBytes(of: &v6) { Array($0) }
            if b.dropLast().allSatisfy({ $0 == 0 }) && b[15] == 1 { return 5 }
            if b[0] == 0xFE && b[1] & 0xC0 == 0x80 { return 4 }
            return 3
        }
        return nil
    }
}
