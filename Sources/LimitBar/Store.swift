import AppKit
import Combine
import Foundation

/// Keeps one Snapshot per vendor fresh at the lowest cost:
/// - serial queue for fetches with strict deadlines;
/// - a source is re-polled only when its own activity files moved (or its reset time passed);
/// - caches results locally; graceful fallback to last known values on transient failures.
final class Store: ObservableObject {
    @Published private(set) var snaps: [Vendor: Snapshot] = [:]
    @Published private(set) var busy: Set<Vendor> = []

    private let queue = DispatchQueue(label: "limitbar.fetch", qos: .utility)
    private var timer: Timer?
    private var asleep = false
    private var lastPoll: [Vendor: Date] = [:]
    private var lastActivity: [Vendor: Date] = [:]
    private var lastClaudeUsage = Date.distantPast
    private var lastAccountCheck = Date.distantPast
    private var lastCodexServer = Date.distantPast
    private var claudeAccount: (signedIn: Bool, plan: String)?

    private var cacheFile: String { "\(Paths.cache)/state.json" }
    private let home = CLI.home

    /// Slow safety net per source even when nothing moved.
    private let idleInterval: [Vendor: TimeInterval] = [
        .claude: 20 * 60, .gemini: 15 * 60, .codex: 30 * 60, .grok: 30 * 60,
        .cursor: 15 * 60, .copilot: 15 * 60, .deepseek: 15 * 60, .ollama: 30
    ]
    /// Minimum gap between polls when activity moved.
    private let busyInterval: [Vendor: TimeInterval] = [
        .claude: 60, .gemini: 60, .codex: 60, .grok: 5 * 60,
        .cursor: 60, .copilot: 60, .deepseek: 60, .ollama: 10
    ]
    /// A missing CLI or app is checked again periodically.
    private let missingInterval: TimeInterval = 5 * 60

    /// Vendors the menu bar shows: everything installed and configured.
    var installed: [Vendor] {
        Vendor.allCases.filter { v in
            guard let s = snaps[v] else { return true }
            return s.state != .missing
        }
    }

    init() {
        loadCache()
        let ws = NSWorkspace.shared.notificationCenter
        ws.addObserver(forName: NSWorkspace.willSleepNotification, object: nil, queue: .main) { [weak self] _ in self?.asleep = true }
        ws.addObserver(forName: NSWorkspace.didWakeNotification, object: nil, queue: .main) { [weak self] _ in
            self?.asleep = false; self?.refresh(force: true)
        }
        timer = Timer.scheduledTimer(withTimeInterval: 30, repeats: true) { [weak self] _ in self?.refresh() }
        timer?.tolerance = 10
        refresh(force: true)
    }

    func refresh(force: Bool = false, olderThan: TimeInterval? = nil) {
        guard !asleep else { return }
        for v in Vendor.allCases where shouldPoll(v, force: force, olderThan: olderThan) {
            schedule(v, force: force)
        }
    }

    private func shouldPoll(_ v: Vendor, force: Bool, olderThan: TimeInterval?) -> Bool {
        if busy.contains(v) { return false }
        let last = lastPoll[v] ?? .distantPast
        let age = Date().timeIntervalSince(last)
        if force { return true }
        if let s = snaps[v], s.state == .missing || s.state == .untrusted { return age > missingInterval }
        if v == .claude { return age > 30 }
        if let o = olderThan { return age > o }
        if let s = snaps[v], s.windows.contains(where: { $0.resetPassed }) { return age > 60 }
        let act = activity(v)
        if let a = act, a > (lastActivity[v] ?? .distantPast), age > busyInterval[v]! { return true }
        return age > idleInterval[v]!
    }

    private func activity(_ v: Vendor) -> Date? {
        let fm = FileManager.default
        var candidates: [String]
        switch v {
        case .claude:
            candidates = [Fetch.claudeFilePath, "\(home)/Library/Application Support/Claude/plan-usage-history.json"]
        case .gemini:
            candidates = [
                "\(home)/Library/Application Support/Antigravity/User/globalStorage/state.vscdb",
                "\(home)/.gemini/antigravity/conversation_summaries.db",
                "\(home)/.gemini/antigravity/conversations",
                "\(home)/.gemini/antigravity-cli/conversations"
            ]
        case .codex:
            let c = Calendar.current, d = Date(), root = "\(home)/.codex/sessions"
            candidates = [root, String(format: "%@/%04d/%02d/%02d", root, c.component(.year, from: d),
                                       c.component(.month, from: d), c.component(.day, from: d))]
        case .grok:
            candidates = ["\(home)/.grok/sessions", "\(home)/Library/Application Support/Grok Bot/sand-client-persistence"]
        case .cursor:
            candidates = ["\(home)/Library/Application Support/Cursor/User/globalStorage/state.vscdb"]
        case .copilot:
            candidates = ["\(home)/.copilot/session-state", "\(home)/.copilot/session-store.db-wal"]
        case .deepseek:
            candidates = ["\(home)/.deepseek/api_key", "\(home)/.config/deepseek/api_key"]
        case .ollama:
            candidates = []
        }
        if v == .grok, let root = candidates.last, let subs = try? fm.contentsOfDirectory(atPath: root) {
            candidates += subs.map { "\(root)/\($0)" }
        }
        return candidates.compactMap { Fetch.mtime($0) }.max()
    }

    private func schedule(_ v: Vendor, force: Bool) {
        busy.insert(v)
        let act = activity(v)
        let prev = snaps[v]
        queue.async { [weak self] in
            guard let self = self else { return }
            let s = self.fetch(v, prev: prev, force: force)
            DispatchQueue.main.async {
                self.busy.remove(v)
                self.lastPoll[v] = Date()
                if let a = act { self.lastActivity[v] = a }
                self.apply(v, s)
            }
        }
    }

    private func fetch(_ v: Vendor, prev: Snapshot?, force: Bool) -> Snapshot {
        switch v {
        case .claude:
            if let fileSnap = Fetch.claudeFile() {
                var s = fileSnap
                if force || Date().timeIntervalSince(lastAccountCheck) > 30 * 60 {
                    lastAccountCheck = Date()
                    claudeAccount = Fetch.claudeAccount() ?? claudeAccount
                }
                if let acct = claudeAccount, !acct.plan.isEmpty { s.plan = acct.plan }
                let fileAge = s.measuredAt.map { Date().timeIntervalSince($0) } ?? .infinity
                if fileAge > 20 * 60, force || Date().timeIntervalSince(lastClaudeUsage) > 20 * 60 {
                    if case .ok = CLI.find("claude") {
                        lastClaudeUsage = Date()
                        let usageSnap = Fetch.claudeUsage()
                        if usageSnap.state == .ok { s = usageSnap }
                    }
                }
                return s
            }
            if case .missing = CLI.find("claude") { return .status(.claude, .missing) }
            if case .untrusted = CLI.find("claude") { return .status(.claude, .untrusted, "claude could be replaced by another account — not run") }
            if force || Date().timeIntervalSince(lastAccountCheck) > 30 * 60 {
                lastAccountCheck = Date()
                claudeAccount = Fetch.claudeAccount() ?? claudeAccount
            }
            if claudeAccount?.signedIn == false { return .status(.claude, .signedOut) }
            var s = Fetch.claudeFile()
            let fileAge = s?.measuredAt.map { Date().timeIntervalSince($0) } ?? .infinity
            if fileAge > 20 * 60, force || Date().timeIntervalSince(lastClaudeUsage) > 20 * 60 {
                lastClaudeUsage = Date()
                s = Fetch.claudeUsage()
            }
            guard var out = s else { return prev ?? .status(.claude, .failed, "waiting for claude") }
            out.plan = claudeAccount?.plan ?? out.plan
            return out
        case .gemini:
            return Fetch.gemini()
        case .codex:
            if case .ok = CLI.find("codex"), !force, let logs = Fetch.codexFromLogs(),
               let m = logs.measuredAt, Date().timeIntervalSince(m) < 10 * 60,
               Date().timeIntervalSince(lastCodexServer) < 30 * 60 {
                var s = logs
                s.notes = prev?.notes ?? []
                return s
            }
            lastCodexServer = Date()
            let s = Fetch.codexFromServer()
            if s.state == .failed, let logs = Fetch.codexFromLogs() { return logs }
            return s
        case .grok:
            return Fetch.grok()
        case .cursor:
            return Fetch.cursor()
        case .copilot:
            return Fetch.copilot()
        case .deepseek:
            return Fetch.deepseek()
        case .ollama:
            return Fetch.ollama()
        }
    }

    private func apply(_ v: Vendor, _ s: Snapshot) {
        var s = s
        if s.state == .failed, var old = snaps[v], old.state == .ok, !old.windows.isEmpty {
            old.error = "Couldn't read — showing last known values" + (s.error.map { " (\($0))" } ?? "")
            s = old
        }
        if s.plan.isEmpty, s.state == .ok { s.plan = snaps[v]?.plan ?? "" }
        snaps[v] = s
        saveCache()
    }

    private func loadCache() {
        guard let d = try? Data(contentsOf: URL(fileURLWithPath: cacheFile)) else { return }
        let dec = JSONDecoder(); dec.dateDecodingStrategy = .iso8601
        if let s = try? dec.decode([String: Snapshot].self, from: d) {
            for (k, v) in s { if let vendor = Vendor(rawValue: k), v.state == .ok { snaps[vendor] = v } }
        }
    }

    private func saveCache() {
        let enc = JSONEncoder(); enc.dateEncodingStrategy = .iso8601
        let dict = Dictionary(uniqueKeysWithValues: snaps.map { ($0.key.rawValue, $0.value) })
        if let d = try? enc.encode(dict) { Paths.write(d, to: cacheFile) }
    }
}
