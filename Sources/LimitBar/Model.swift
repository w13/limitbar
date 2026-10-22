import Foundation

enum Vendor: String, CaseIterable, Codable {
    case claude, gemini, codex, grok, cursor, copilot, deepseek, ollama

    var name: String {
        switch self {
        case .claude: return "Claude"
        case .gemini: return "Antigravity"
        case .codex: return "Codex"
        case .grok: return "Grok"
        case .cursor: return "Cursor"
        case .copilot: return "Copilot"
        case .deepseek: return "DeepSeek"
        case .ollama: return "Ollama"
        }
    }

    /// The CLI or tool limitbar asks, and what the panel suggests when it is missing or signed out.
    var cli: String {
        switch self {
        case .claude: return "claude"
        case .gemini: return "agy"
        case .codex: return "codex"
        case .grok: return "grok"
        case .cursor: return "cursor"
        case .copilot: return "copilot"
        case .deepseek: return "deepseek"
        case .ollama: return "ollama"
        }
    }

    var product: String {
        switch self {
        case .claude: return "Claude Code & Desktop"
        case .gemini: return "Google Antigravity & Gemini"
        case .codex: return "Codex CLI & ChatGPT"
        case .grok: return "Grok Bot & xAI"
        case .cursor: return "Cursor IDE"
        case .copilot: return "GitHub Copilot CLI"
        case .deepseek: return "DeepSeek API"
        case .ollama: return "Ollama Local AI"
        }
    }

    var appBundleId: String? {
        switch self {
        case .claude: return "com.anthropic.claudefordesktop"
        case .gemini: return "com.google.antigravity"
        case .codex: return "com.openai.codex"
        case .grok: return "com.anysphere.sand"
        case .cursor: return "com.todesktop.230313mzl4w4u92"
        case .ollama: return "com.electron.ollama"
        default: return nil
        }
    }

    var loginHint: String {
        switch self {
        case .claude: return "open Claude.app or run `claude`"
        case .gemini: return "open Antigravity.app or run `agy`"
        case .codex: return "open ChatGPT.app or run `codex login`"
        case .grok: return "open Grok Bot.app or run `grok login`"
        case .cursor: return "sign in through Cursor.app"
        case .copilot: return "run `copilot login` or `gh auth login`"
        case .deepseek: return "configure API key in panel"
        case .ollama: return "run `ollama serve` (http://localhost:11434)"
        }
    }

    var mark: String {
        switch self {
        case .claude: return "claude"
        case .gemini: return "gemini"
        case .codex: return "openai"
        case .grok: return "xai"
        case .cursor: return "cursor"
        case .copilot: return "copilot"
        case .deepseek: return "deepseek"
        case .ollama: return "ollama"
        }
    }
}

/// Why a vendor shows no numbers. `ok` = the CLI or desktop integration answered.
enum VendorState: String, Codable {
    case ok
    case missing        // CLI or app not found: hidden from the menu bar, listed in the panel
    case untrusted      // found, but owned by another account or group/world-writable: never run
    case signedOut      // the CLI or app says nobody is signed in
    case failed         // ran but did not return limits (timeout, error, changed format)
}

/// One limit window as the vendor reports it. `used` is percent USED, 0–100.
struct Window: Codable, Equatable {
    var label: String            // "5-hour", "Week", "Weekly credits", "Fast requests" …
    var short: String            // "5h", "w", "m", "$"
    var used: Double?
    var resetsAt: Date?

    /// The reset time has passed since we read it: the old percent is obsolete, not "0 %".
    var resetPassed: Bool { (resetsAt.map { $0 < Date() }) ?? false }
}

struct Snapshot: Codable, Equatable {
    var vendor: Vendor
    var state: VendorState = .ok
    var plan: String = ""         // from the CLI or app: "Max", "Cursor Pro", "Google AI Pro", "ChatGPT Free"
    var bar: Window?              // the window the menu bar shows for this vendor
    var windows: [Window] = []    // everything shown in the panel, bar window first
    var notes: [String] = []      // notes, e.g. "0 / 500 requests used"
    var measuredAt: Date?         // when the vendor's numbers were taken
    var error: String?            // last failure, shown dimmed; previous numbers are kept

    /// Any window of this vendor at ≥ 90 % other than the one in the bar → "!" next to the digit.
    var otherHot: Bool {
        windows.contains { $0 != bar && !$0.resetPassed && ($0.used ?? 0) >= 90 }
    }

    static func status(_ v: Vendor, _ state: VendorState, _ error: String? = nil) -> Snapshot {
        Snapshot(vendor: v, state: state, error: error)
    }
}

/// A percentage from a CLI or app is only used when it is a finite number in 0…100.
func percent(_ any: Any?) -> Double? {
    let v: Double?
    switch any {
    case let d as Double: v = d
    case let i as Int: v = Double(i)
    case let n as NSNumber: v = n.doubleValue
    default: v = nil
    }
    guard let x = v, x.isFinite, x >= 0, x <= 100 else { return nil }
    return x
}

/// Bar digit: rounded, but 99.6 % must never read as 100 %.
func percentText(_ v: Double?) -> String {
    guard let v = v else { return "—" }
    let r = v.rounded()
    return String(Int(r >= 100 && v < 100 ? 99 : r))
}
