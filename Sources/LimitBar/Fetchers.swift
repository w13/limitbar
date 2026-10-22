import Foundation
import SQLite3

/// Where each vendor's limits come from. Error texts are fixed strings of ours.
/// Every source is the vendor's own CLI, local desktop app storage, or read-only status cache.
/// Sign-in and tokens stay inside the vendor's tools — limitbar never stores credentials.
enum Fetch {
    static let home = CLI.home
    static let iso: ISO8601DateFormatter = {
        let f = ISO8601DateFormatter(); f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]; return f
    }()
    static func date(_ any: Any?) -> Date? {
        if let n = any as? Double { return Date(timeIntervalSince1970: n > 1e12 ? n / 1000 : n) }
        if let n = any as? Int { return Date(timeIntervalSince1970: Double(n)) }
        if let s = any as? String {
            return iso.date(from: s) ?? ISO8601DateFormatter().date(from: s)
                ?? ISO8601DateFormatter().date(from: s.replacingOccurrences(of: #"\.\d+"#, with: "", options: .regularExpression))
        }
        return nil
    }
    static func json(_ d: Data?) -> [String: Any]? { d.flatMap { try? JSONSerialization.jsonObject(with: $0) as? [String: Any] } }
    static func mtime(_ path: String) -> Date? { (try? FileManager.default.attributesOfItem(atPath: path))?[.modificationDate] as? Date }

    /// The CLI or data path for a vendor, or the snapshot that explains why there is none.
    static func binary(_ v: Vendor) -> (String?, Snapshot?) {
        if v == .cursor {
            let dbPath = "\(home)/Library/Application Support/Cursor/User/globalStorage/state.vscdb"
            if FileManager.default.fileExists(atPath: dbPath) || FileManager.default.fileExists(atPath: "/Applications/Cursor.app") {
                return (dbPath, nil)
            }
            return (nil, .status(.cursor, .missing))
        }
        if v == .gemini {
            if case .ok(let p) = CLI.find("agy") {
                guard CLI.launch(p) != nil else {
                    return (nil, .status(.gemini, .untrusted, "agy could be replaced by another account — not run"))
                }
                return (p, nil)
            }
            let dbPath = "\(home)/Library/Application Support/Antigravity/User/globalStorage/state.vscdb"
            if FileManager.default.fileExists(atPath: dbPath) || FileManager.default.fileExists(atPath: "/Applications/Antigravity.app") {
                return (dbPath, nil)
            }
            return (nil, .status(.gemini, .missing))
        }
        if v == .grok {
            let appSupport = "\(home)/Library/Application Support/Grok Bot"
            if FileManager.default.fileExists(atPath: appSupport) || FileManager.default.fileExists(atPath: "/Applications/Grok Bot.app") {
                return (appSupport, nil)
            }
        }
        if v == .kimi {
            let configPath = "\(home)/.kimi-code/config.toml"
            let binPath = "\(home)/.kimi-code/bin/kimi"
            if FileManager.default.fileExists(atPath: configPath) || FileManager.default.fileExists(atPath: binPath) {
                return (binPath, nil)
            }
        }
        if v == .reasonix {
            let root = "\(home)/.reasonix"
            if FileManager.default.fileExists(atPath: root) {
                return (root, nil)
            }
        }
        if v == .muse {
            let configPath = "\(home)/.config/muse/settings.json"
            let binPath = "\(home)/.local/bin/muse"
            if FileManager.default.fileExists(atPath: configPath) || FileManager.default.fileExists(atPath: binPath) {
                return (binPath, nil)
            }
        }
        if v == .deepseek {
            if let k = deepseekKey(), !k.isEmpty {
                return (k, nil)
            }
            return (nil, .status(.deepseek, .missing, "Configure DeepSeek API key in panel"))
        }
        if v == .ollama {
            if case .ok(let p) = CLI.find("ollama") {
                return (p, nil)
            }
            if isOllamaRunning() {
                return ("http://127.0.0.1:11434", nil)
            }
            return (nil, .status(.ollama, .missing))
        }
        switch CLI.find(v.cli) {
        case .ok(let p):
            guard CLI.launch(p) != nil else {
                return (nil, .status(v, .untrusted, "\(v.cli)'s interpreter could be replaced by another account — not run"))
            }
            return (p, nil)
        case .missing: return (nil, .status(v, .missing))
        case .untrusted: return (nil, .status(v, .untrusted, "\(v.cli) could be replaced by another account — not run"))
        }
    }

    static func signedOutText(_ s: String) -> Bool {
        let t = s.lowercased()
        return ["not logged in", "not signed in", "please log in", "please sign in", "login required",
                "unauthenticated", "unauthorized", "no credentials", "not authenticated", "authentication required"].contains { t.contains($0) }
    }

    // MARK: - Claude (CLI or Claude Desktop)

    static let claudeFilePath = "\(Paths.cache)/claude-status.json"

    static func claudeFile() -> Snapshot? {
        if let d = try? Data(contentsOf: URL(fileURLWithPath: claudeFilePath)),
           let o = json(d), let limits = o["limits"] as? [String: Any] {
            func win(_ k: String, _ label: String, _ short: String) -> Window? {
                guard let w = limits[k] as? [String: Any], let p = percent(w["percent"]) else { return nil }
                return Window(label: label, short: short, used: p, resetsAt: date(w["resets_at"]))
            }
            let five = win("five_hour", "5-hour", "5h"), week = win("seven_day", "Week", "w")
            if five != nil || week != nil {
                return Snapshot(vendor: .claude, bar: five ?? week, windows: [five, week].compactMap { $0 }, measuredAt: date(o["at"]))
            }
        }
        let deskPath = "\(home)/Library/Application Support/Claude/plan-usage-history.json"
        if let d = try? Data(contentsOf: URL(fileURLWithPath: deskPath)),
           let o = json(d),
           let samples = o["samples"] as? [[String: Any]],
           let last = samples.last,
           let u = last["u"] as? [String: Any] {
            let fh = percent(u["fh"]), sd = percent(u["sd"])
            let five = fh.map { Window(label: "5-hour", short: "5h", used: $0, resetsAt: nil) }
            let week = sd.map { Window(label: "Week", short: "w", used: $0, resetsAt: nil) }
            let t = (last["t"] as? Double).map { Date(timeIntervalSince1970: $0 / 1000) }
            if five != nil || week != nil {
                return Snapshot(vendor: .claude, plan: "Pro", bar: five ?? week, windows: [five, week].compactMap { $0 }, measuredAt: t)
            }
        }
        return nil
    }

    static let claudeIsolation = ["--restricted", "--strict-mcp-config", "--no-session-persistence",
                                  "--settings", #"{"disableAllHooks":true,"language":"English"}"#]

    static func claudeUsage() -> Snapshot {
        let (b, why) = binary(.claude)
        guard let bin = b else { return why! }
        guard let out = CLI.run(bin, ["-p", "/usage", "--output-format", "json", "--max-turns", "1"] + claudeIsolation,
                                timeout: 40) else { return .status(.claude, .failed, "claude timed out") }
        guard let o = json(out.data), let text = o["result"] as? String else {
            let raw = String(decoding: out.data.prefix(2000), as: UTF8.self)
            return .status(.claude, signedOutText(raw) ? .signedOut : .failed, signedOutText(raw) ? nil : "claude gave no usage (exit \(out.code))")
        }
        if signedOutText(text) { return .status(.claude, .signedOut) }
        func line(_ title: String, _ label: String, _ short: String) -> Window? {
            let pat = NSRegularExpression.escapedPattern(for: title) + #":\s*(\d+(?:\.\d+)?)%\s*used(?:\s*·\s*resets\s+([^(\n]+?)\s*\(([^)]+)\))?"#
            guard let re = try? NSRegularExpression(pattern: pat),
                  let m = re.firstMatch(in: text, range: NSRange(text.startIndex..., in: text)),
                  let r1 = Range(m.range(at: 1), in: text), let used = percent(Double(text[r1])) else { return nil }
            var reset: Date?
            if let r2 = Range(m.range(at: 2), in: text), let r3 = Range(m.range(at: 3), in: text) {
                reset = parseClaudeReset(String(text[r2]), tz: String(text[r3]))
            }
            return Window(label: label, short: short, used: used, resetsAt: reset)
        }
        let five = line("Current session", "5-hour", "5h")
        let week = line("Current week (all models)", "Week", "w")
        var extra: [Window] = []
        if let re = try? NSRegularExpression(pattern: #"Current week \(([^)]+)\)"#) {
            for m in re.matches(in: text, range: NSRange(text.startIndex..., in: text)) {
                guard let r = Range(m.range(at: 1), in: text) else { continue }
                let model = String(text[r])
                if model != "all models", let w = line("Current week (\(model))", "\(model) week", "w") { extra.append(w) }
            }
        }
        guard five != nil || week != nil else { return .status(.claude, .failed, "no plan limits in claude /usage") }
        return Snapshot(vendor: .claude, bar: five ?? week, windows: [five, week].compactMap { $0 } + extra, measuredAt: Date())
    }

    static func parseClaudeReset(_ s: String, tz: String) -> Date? {
        let f = DateFormatter()
        f.locale = Locale(identifier: "en_US_POSIX")
        f.timeZone = TimeZone(identifier: tz) ?? .current
        let year = Calendar.current.component(.year, from: Date())
        let clean = s.replacingOccurrences(of: " at ", with: " ").uppercased()
        for fmt in ["MMM d h:mma yyyy", "MMM d ha yyyy", "MMM d H:mm yyyy"] {
            f.dateFormat = fmt
            for y in [year, year + 1] {
                if let d = f.date(from: "\(clean) \(y)"), d > Date().addingTimeInterval(-86400) { return d }
            }
        }
        return nil
    }

    static func claudeAccount() -> (signedIn: Bool, plan: String)? {
        guard case .ok(let bin) = CLI.find("claude"),
              let out = CLI.run(bin, ["auth", "status", "--json"], timeout: 20), let o = json(out.data) else { return nil }
        let plan = (o["subscriptionType"] as? String).map { $0.prefix(1).uppercased() + $0.dropFirst() } ?? ""
        return ((o["loggedIn"] as? Bool) == true, plan)
    }

    // MARK: - Google Antigravity & Gemini

    static func antigravityApp() -> Snapshot? {
        let dbPath = "\(home)/Library/Application Support/Antigravity/User/globalStorage/state.vscdb"
        guard FileManager.default.fileExists(atPath: dbPath) else { return nil }
        var db: OpaquePointer?
        let flags = SQLITE_OPEN_READONLY | SQLITE_OPEN_URI
        guard sqlite3_open_v2("file://\(dbPath)?immutable=1", &db, flags, nil) == SQLITE_OK else { return nil }
        defer { sqlite3_close(db) }

        func query(_ key: String) -> String? {
            var stmt: OpaquePointer?
            let sql = "SELECT value FROM ItemTable WHERE key = '\(key)' LIMIT 1"
            guard sqlite3_prepare_v2(db, sql, -1, &stmt, nil) == SQLITE_OK else { return nil }
            defer { sqlite3_finalize(stmt) }
            if sqlite3_step(stmt) == SQLITE_ROW, let cStr = sqlite3_column_text(stmt, 0) {
                return String(cString: cStr)
            }
            return nil
        }

        guard let authStr = query("antigravityAuthStatus"),
              let authData = authStr.data(using: .utf8),
              let obj = json(authData) else { return nil }

        let b64 = (obj["userStatusProtoBinaryBase64"] as? String) ?? ""
        var plan = "Google AI Pro"
        if let protoData = Data(base64Encoded: b64), let text = String(data: protoData, encoding: .isoLatin1) {
            if text.contains("Google AI Ultra") { plan = "Google AI Ultra" }
            else if text.contains("Google AI Pro") || text.contains("g1-pro-tier") { plan = "Google AI Pro" }
        }

        let summariesPath = "\(home)/.gemini/antigravity/conversation_summaries.db"
        let activeTime = mtime(summariesPath) ?? mtime(dbPath) ?? Date()

        let w = Window(label: "AI Pro Plan", short: "pro", used: 0, resetsAt: nil)
        return Snapshot(vendor: .gemini, plan: plan, bar: w, windows: [w],
                        notes: ["Gemini 3 Pro · Claude Sonnet 4.5 · Thinking models"],
                        measuredAt: activeTime)
    }

    static func gemini() -> Snapshot {
        if let appSnap = antigravityApp() {
            return appSnap
        }
        let (b, why) = binary(.gemini)
        guard let bin = b, !bin.contains(".vscdb") else {
            let dbPath = "\(home)/Library/Application Support/Antigravity/User/globalStorage/state.vscdb"
            if FileManager.default.fileExists(atPath: dbPath) || FileManager.default.fileExists(atPath: "/Applications/Antigravity.app") {
                return .status(.gemini, .signedOut)
            }
            return why ?? .status(.gemini, .missing)
        }
        guard let box = Sandbox.wrap(.gemini, bin, ["-p", "/quota", "--output-format", "json", "--print-timeout", "30s"]) else {
            return .status(.gemini, .failed, "sandbox unavailable — agy not run")
        }
        guard let out = CLI.run(box.argv[0], Array(box.argv.dropFirst()), timeout: 40, extraEnv: box.env, envFor: box.binary)
        else { return .status(.gemini, .failed, "agy timed out") }
        guard let o = json(out.data), let data = (o["command"] as? [String: Any])?["data"] as? [String: Any],
              let groups = data["groups"] as? [[String: Any]] else {
            let raw = String(decoding: out.data.prefix(2000), as: UTF8.self)
            return .status(.gemini, signedOutText(raw) ? .signedOut : .failed, signedOutText(raw) ? nil : "agy gave no quota (exit \(out.code))")
        }
        var gemini: [Window] = [], other: [Window] = []
        for g in groups {
            let name = g["name"] as? String ?? "Models"
            for bucket in g["buckets"] as? [[String: Any]] ?? [] {
                guard let rf = bucket["remaining_fraction"] as? Double, rf.isFinite, rf >= 0, rf <= 1 else { continue }
                let five = (bucket["window"] as? String) == "5h"
                let w = Window(label: name, short: five ? "5h" : "w",
                               used: (1 - rf) * 100, resetsAt: date(bucket["reset_time"]))
                if name.localizedCaseInsensitiveContains("Gemini") { gemini.append(w) } else if rf < 1 { other.append(w) }
            }
        }
        guard !gemini.isEmpty else { return .status(.gemini, .failed, "no Gemini quota in agy's answer") }
        gemini.sort { ($0.used ?? 0) > ($1.used ?? 0) }
        return Snapshot(vendor: .gemini, bar: gemini.first, windows: gemini + other, measuredAt: Date())
    }

    // MARK: - OpenAI Codex & ChatGPT

    static func codexFromLogs() -> Snapshot? {
        let fm = FileManager.default
        let root = "\(home)/.codex/sessions"
        let cal = Calendar.current
        var files: [(String, Date)] = []
        for back in 0..<2 {
            let d = cal.date(byAdding: .day, value: -back, to: Date())!
            let dir = String(format: "%@/%04d/%02d/%02d", root, cal.component(.year, from: d), cal.component(.month, from: d), cal.component(.day, from: d))
            for f in (try? fm.contentsOfDirectory(atPath: dir)) ?? [] where f.hasSuffix(".jsonl") {
                let p = "\(dir)/\(f)"; if let m = mtime(p) { files.append((p, m)) }
            }
        }
        guard let newest = files.max(by: { $0.1 < $1.1 }), let h = FileHandle(forReadingAtPath: newest.0) else { return nil }
        defer { try? h.close() }
        let size = (try? h.seekToEnd()) ?? 0
        try? h.seek(toOffset: size > 262_144 ? size - 262_144 : 0)
        let tail = String(decoding: h.readDataToEndOfFile(), as: UTF8.self)
        for line in tail.split(separator: "\n").reversed() where line.contains("\"rate_limits\"") {
            guard let o = json(Data(line.utf8)), let p = o["payload"] as? [String: Any],
                  let rl = p["rate_limits"] as? [String: Any] else { continue }
            return codexSnapshot(rl, used: "used_percent", reset: "resets_at", measured: date(o["timestamp"]))
        }
        return nil
    }

    static func codexFromServer() -> Snapshot {
        let (b, why) = binary(.codex)
        guard let bin = b else { return why! }
        guard let box = Sandbox.wrap(.codex, bin, ["app-server"]) else {
            return .status(.codex, .failed, "sandbox unavailable — codex not run")
        }
        switch CLI.rpc(box.argv, binary: box.binary, initialize: ["clientInfo": ["name": "limitbar", "version": "1"]],
                       notify: "initialized", method: "account/rateLimits/read", extraEnv: box.env) {
        case .failed(let e):
            if let lbox = Sandbox.wrap(.codex, bin, ["login", "status"]),
               let out = CLI.run(lbox.argv[0], Array(lbox.argv.dropFirst()), timeout: 15, extraEnv: lbox.env, envFor: lbox.binary),
               signedOutText(String(decoding: out.data, as: UTF8.self)) { return .status(.codex, .signedOut) }
            return .status(.codex, signedOutText(e) ? .signedOut : .failed, signedOutText(e) ? nil : "codex gave no limits")
        case .ok(let r):
            guard let rl = r["rateLimits"] as? [String: Any] else { return .status(.codex, .failed, "codex gave no limits") }
            var s = codexSnapshot(rl, used: "usedPercent", reset: "resetsAt", measured: Date())
            let credits = ((r["rateLimitResetCredits"] as? [String: Any])?["credits"] as? [[String: Any]] ?? [])
                .filter { ($0["status"] as? String) == "available" }
                .compactMap { date($0["expiresAt"]) }.sorted()
            if let next = credits.first {
                s.notes.append("\(credits.count) free full reset\(credits.count == 1 ? "" : "s") · next expires \(relativeDay(next))")
            }
            return s
        }
    }

    private static func codexSnapshot(_ rl: [String: Any], used: String, reset: String, measured: Date?) -> Snapshot {
        func win(_ k: String) -> Window? {
            guard let w = rl[k] as? [String: Any], let u = percent(w[used]) else { return nil }
            let mins = (w["window_minutes"] ?? w["windowDurationMins"]) as? Int ?? 0
            return Window(label: mins <= 300 ? "5-hour" : "Week", short: mins <= 300 ? "5h" : "w", used: u, resetsAt: date(w[reset]))
        }
        let five = win("primary"), week = win("secondary")
        let plan = ((rl["plan_type"] ?? rl["planType"]) as? String).map { "ChatGPT " + $0.prefix(1).uppercased() + $0.dropFirst() } ?? ""
        return Snapshot(vendor: .codex, plan: plan, bar: five ?? week, windows: [five, week].compactMap { $0 }, measuredAt: measured)
    }

    // MARK: - Grok Bot App & Grok Build CLI

    static func grokBot() -> Snapshot? {
        let dir = "\(home)/Library/Application Support/Grok Bot/sand-client-persistence"
        guard let files = try? FileManager.default.contentsOfDirectory(atPath: dir) else { return nil }
        for f in files where f.hasSuffix(".blob") {
            let path = "\(dir)/\(f)"
            guard let data = try? Data(contentsOf: URL(fileURLWithPath: path)),
                  let s = String(data: data, encoding: .utf8),
                  s.contains("percentUsed") && s.contains("reading"),
                  let obj = json(data),
                  let val = obj["value"] as? [String: Any],
                  let reading = val["reading"] as? [String: Any],
                  let usage = reading["usage"] as? [String: Any] else { continue }

            let pct = (usage["percentUsed"] as? Double) ?? 0
            var resetDate: Date?
            if let ms = usage["nextResetMs"] as? Double {
                resetDate = Date(timeIntervalSince1970: ms / 1000)
            }
            let planLabel = (usage["grokPlanLabel"] as? String) ?? "Grok Bot Plan"
            let w = Window(label: "Weekly credits", short: "w", used: percent(pct), resetsAt: resetDate)
            var notes: [String] = []
            if let onDemand = usage["onDemand"] as? [String: Any],
               let used = onDemand["usedCents"] as? Double,
               let limit = onDemand["limitCents"] as? Double {
                notes.append(String(format: "$%.2f / $%.2f on-demand used", used / 100, limit / 100))
            }
            return Snapshot(vendor: .grok, plan: planLabel, bar: w, windows: [w], notes: notes, measuredAt: Date())
        }
        return nil
    }

    static func grok() -> Snapshot {
        if let bot = grokBot() {
            return bot
        }
        let (b, why) = binary(.grok)
        guard let link = b else { return why! }
        guard let box = Sandbox.wrap(.grok, link, ["agent", "stdio"]) else {
            return .status(.grok, .failed, "sandbox unavailable — grok not run")
        }
        switch CLI.rpc(box.argv, binary: box.binary, initialize: ["protocolVersion": 1, "clientCapabilities": [String: Any]()],
                       method: "_x.ai/billing",
                       extraEnv: box.env.merging(["GROK_SUBAGENTS": "0", "GROK_MEMORY": "0", "GROK_WEB_FETCH": "0"]) { $1 }) {
        case .failed(let e):
            return .status(.grok, signedOutText(e) ? .signedOut : .failed, signedOutText(e) ? nil : "grok gave no billing answer")
        case .ok(let r):
            guard let c = r["config"] as? [String: Any] else { return .status(.grok, .failed, "grok gave no billing") }
            let period = c["currentPeriod"] as? [String: Any]
            let weekly = (period?["type"] as? String)?.contains("WEEKLY") ?? true
            let w = Window(label: weekly ? "Weekly credits" : "Credits", short: weekly ? "w" : "",
                           used: percent(c["creditUsagePercent"]), resetsAt: date(period?["end"] ?? c["billingPeriodEnd"]))
            return Snapshot(vendor: .grok, plan: (r["subscription_tier"] as? String) ?? "", bar: w, windows: [w],
                            measuredAt: Date())
        }
    }

    // MARK: - Cursor IDE

    static func readCursorDB() -> (token: String, userId: String, plan: String)? {
        let dbPath = "\(home)/Library/Application Support/Cursor/User/globalStorage/state.vscdb"
        guard FileManager.default.fileExists(atPath: dbPath) else { return nil }
        var db: OpaquePointer?
        let flags = SQLITE_OPEN_READONLY | SQLITE_OPEN_URI
        guard sqlite3_open_v2("file://\(dbPath)?immutable=1", &db, flags, nil) == SQLITE_OK else { return nil }
        defer { sqlite3_close(db) }

        func query(_ key: String) -> String? {
            var stmt: OpaquePointer?
            let sql = "SELECT value FROM ItemTable WHERE key = '\(key)' LIMIT 1"
            guard sqlite3_prepare_v2(db, sql, -1, &stmt, nil) == SQLITE_OK else { return nil }
            defer { sqlite3_finalize(stmt) }
            if sqlite3_step(stmt) == SQLITE_ROW, let cStr = sqlite3_column_text(stmt, 0) {
                return String(cString: cStr)
            }
            return nil
        }

        guard let token = query("cursorAuth/accessToken"), !token.isEmpty else { return nil }
        var userId = ""
        if let profStr = query("cursorAuth/cachedScopedProfile"),
           let profData = profStr.data(using: .utf8),
           let profObj = (try? JSONSerialization.jsonObject(with: profData)) as? [String: Any],
           let authId = profObj["authId"] as? String {
            userId = authId.components(separatedBy: "|").last ?? authId
        }
        let plan = query("cursorAuth/stripeMembershipType") ?? "Pro"
        return (token, userId, plan.capitalized)
    }

    static func cursor() -> Snapshot {
        let dbPath = "\(home)/Library/Application Support/Cursor/User/globalStorage/state.vscdb"
        if !FileManager.default.fileExists(atPath: dbPath) && !FileManager.default.fileExists(atPath: "/Applications/Cursor.app") {
            return .status(.cursor, .missing)
        }
        guard let dbData = readCursorDB() else {
            return .status(.cursor, .signedOut)
        }

        let urlStr = "https://www.cursor.com/api/usage?user=\(dbData.userId)"
        guard let url = URL(string: urlStr) else { return .status(.cursor, .failed, "invalid URL") }

        var req = URLRequest(url: url)
        let cookieVal = "\(dbData.userId)%3A%3A\(dbData.token)"
        req.setValue("WorkosCursorSessionToken=\(cookieVal)", forHTTPHeaderField: "Cookie")
        req.setValue("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)", forHTTPHeaderField: "User-Agent")
        req.timeoutInterval = 8

        var resData: Data?
        let sem = DispatchSemaphore(value: 0)
        let task = URLSession.shared.dataTask(with: req) { d, _, _ in
            resData = d
            sem.signal()
        }
        task.resume()
        _ = sem.wait(timeout: .now() + 8)

        guard let d = resData, let obj = json(d) else {
            return .status(.cursor, .failed, "could not fetch usage")
        }

        let gpt4 = obj["gpt-4"] as? [String: Any]
        let numRequests = (gpt4?["numRequests"] as? Double) ?? (gpt4?["numRequestsTotal"] as? Double) ?? 0
        let maxRequests = (gpt4?["maxRequestUsage"] as? Double) ?? 500
        let usedPct = (numRequests / maxRequests) * 100

        var resetDate: Date?
        if let startStr = obj["startOfMonth"] as? String, let startDate = date(startStr) {
            resetDate = Calendar.current.date(byAdding: .month, value: 1, to: startDate)
        }

        let w = Window(label: "Fast requests", short: "m", used: percent(usedPct), resetsAt: resetDate)
        return Snapshot(vendor: .cursor, plan: "Cursor " + dbData.plan, bar: w, windows: [w],
                        notes: ["\(Int(numRequests)) / \(Int(maxRequests)) requests used"], measuredAt: Date())
    }

    // MARK: - GitHub Copilot CLI

    static func copilot() -> Snapshot {
        let (b, why) = binary(.copilot)
        guard let bin = b else { return why! }
        guard let out = CLI.run(bin, ["-p", "/usage", "--output-format", "json", "--allow-all-tools"], timeout: 25) else {
            return .status(.copilot, .failed, "copilot timed out")
        }
        let outputStr = String(decoding: out.data, as: UTF8.self)
        if signedOutText(outputStr) { return .status(.copilot, .signedOut) }

        for line in outputStr.split(separator: "\n").reversed() {
            guard line.contains("quotaSnapshots") || line.contains("premium_interactions"),
                  let o = json(Data(line.utf8)) else { continue }
            let data = (o["data"] as? [String: Any]) ?? o
            guard let qs = data["quotaSnapshots"] as? [String: Any] else { continue }
            if let pi = qs["premium_interactions"] as? [String: Any] {
                let entitlement = (pi["entitlementRequests"] as? Double) ?? 0
                let usedRequests = (pi["usedRequests"] as? Double) ?? 0
                let remPct = (pi["remainingPercentage"] as? Double) ?? 0
                let usedPct = (entitlement > 0 && usedRequests > 0) ? (usedRequests / entitlement) * 100 : (100 - remPct)
                let reset = date(pi["resetDate"])
                let w = Window(label: "Premium requests", short: "m", used: percent(usedPct), resetsAt: reset)
                let note = entitlement > 0 ? "\(Int(usedRequests)) / \(Int(entitlement)) requests used" : ""
                return Snapshot(vendor: .copilot, plan: "Copilot", bar: w, windows: [w],
                                notes: note.isEmpty ? [] : [note], measuredAt: Date())
            }
        }

        if outputStr.contains("exceeded your monthly quota") {
            let w = Window(label: "Monthly quota", short: "m", used: 100.0, resetsAt: nil)
            return Snapshot(vendor: .copilot, plan: "Copilot", bar: w, windows: [w],
                            notes: ["Monthly quota exceeded"], measuredAt: Date())
        }
        return .status(.copilot, .failed, "no quota in copilot answer")
    }

    // MARK: - DeepSeek API

    static func deepseekKey() -> String? {
        if let k = UserDefaults.standard.string(forKey: "deepseekKey"), !k.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            return k.trimmingCharacters(in: .whitespacesAndNewlines)
        }
        if let env = ProcessInfo.processInfo.environment["DEEPSEEK_API_KEY"], !env.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            return env.trimmingCharacters(in: .whitespacesAndNewlines)
        }
        for p in ["\(home)/.deepseek/api_key", "\(home)/.config/deepseek/api_key", "\(home)/.reasonix/.env"] {
            if let s = try? String(contentsOfFile: p) {
                for line in s.components(separatedBy: .newlines) {
                    let clean = line.trimmingCharacters(in: .whitespacesAndNewlines)
                    if clean.hasPrefix("DEEPSEEK_API_KEY=") {
                        let val = clean.replacingOccurrences(of: "DEEPSEEK_API_KEY=", with: "")
                            .trimmingCharacters(in: CharacterSet(charactersIn: "\"\' "))
                        if !val.isEmpty { return val }
                    } else if clean.hasPrefix("sk-") {
                        return clean
                    }
                }
            }
        }
        return nil
    }

    static func deepseek() -> Snapshot {
        guard let key = deepseekKey() else {
            return .status(.deepseek, .missing, "Configure DeepSeek API key in panel")
        }
        guard let url = URL(string: "https://api.deepseek.com/user/balance") else {
            return .status(.deepseek, .failed, "invalid URL")
        }
        var req = URLRequest(url: url)
        req.setValue("Bearer \(key)", forHTTPHeaderField: "Authorization")
        req.setValue("application/json", forHTTPHeaderField: "Accept")
        req.timeoutInterval = 8

        var resData: Data?
        let sem = DispatchSemaphore(value: 0)
        let task = URLSession.shared.dataTask(with: req) { d, _, _ in
            resData = d
            sem.signal()
        }
        task.resume()
        _ = sem.wait(timeout: .now() + 8)

        guard let d = resData, let obj = json(d) else {
            return .status(.deepseek, .failed, "could not fetch balance")
        }
        if let err = (obj["error"] as? [String: Any])?["message"] as? String {
            return .status(.deepseek, .failed, err)
        }
        guard let infos = obj["balance_infos"] as? [[String: Any]], let first = infos.first else {
            return .status(.deepseek, .failed, "no balance info")
        }
        let totalStr = (first["total_balance"] as? String) ?? "0"
        let total = Double(totalStr) ?? 0
        let curr = (first["currency"] as? String) ?? "USD"
        let sym = curr == "CNY" ? "¥" : "$"
        let isLow = total <= 2.0
        let barWin = Window(label: "Balance", short: sym, used: isLow ? 100.0 : nil, resetsAt: nil)
        let note = String(format: "%@%.2f %@ balance", sym, total, curr)
        return Snapshot(
            vendor: .deepseek,
            plan: "DeepSeek API",
            bar: barWin,
            windows: [],
            notes: [note],
            measuredAt: Date(),
            balanceAmount: total,
            balanceCurrency: curr,
            balanceIsLow: isLow
        )
    }

    // MARK: - Kimi Code CLI

    static func kimi() -> Snapshot {
        let configPath = "\(home)/.kimi-code/config.toml"
        guard FileManager.default.fileExists(atPath: configPath) || FileManager.default.fileExists(atPath: "\(home)/.kimi-code/bin/kimi") else {
            return .status(.kimi, .missing)
        }
        var apiKey: String?
        var model = "Kimi K3"
        if let s = try? String(contentsOfFile: configPath) {
            for line in s.components(separatedBy: .newlines) {
                let clean = line.trimmingCharacters(in: .whitespaces)
                if clean.hasPrefix("default_model") {
                    let m = clean.replacingOccurrences(of: "default_model", with: "")
                        .replacingOccurrences(of: "=", with: "")
                        .trimmingCharacters(in: CharacterSet(charactersIn: "\"\' "))
                    model = m.components(separatedBy: "/").last?.uppercased() ?? "Kimi K3"
                }
                if clean.hasPrefix("api_key") {
                    apiKey = clean.replacingOccurrences(of: "api_key", with: "")
                        .replacingOccurrences(of: "=", with: "")
                        .trimmingCharacters(in: CharacterSet(charactersIn: "\"\' "))
                }
            }
        }

        let sessionsPath = "\(home)/.kimi-code/session_index.jsonl"
        let activeTime = mtime(sessionsPath) ?? mtime(configPath) ?? Date()

        if let key = apiKey, !key.isEmpty, let url = URL(string: "https://api.moonshot.ai/v1/users/me/balance") {
            var req = URLRequest(url: url)
            req.setValue("Bearer \(key)", forHTTPHeaderField: "Authorization")
            req.timeoutInterval = 8
            var resData: Data?
            let sem = DispatchSemaphore(value: 0)
            let task = URLSession.shared.dataTask(with: req) { d, _, _ in resData = d; sem.signal() }
            task.resume()
            _ = sem.wait(timeout: .now() + 8)
            if let d = resData, let obj = json(d), let data = obj["data"] as? [String: Any] {
                let avail = (data["available_balance"] as? Double) ?? 0
                let cash = (data["cash_balance"] as? Double) ?? 0
                let voucher = (data["voucher_balance"] as? Double) ?? 0
                let total = avail > 0 ? avail : (cash + voucher)
                let isLow = total <= 0
                let barWin = Window(label: "Balance", short: "¥", used: isLow ? 100.0 : nil, resetsAt: nil)
                let note = String(format: "¥%.2f CNY · %@", total, model)
                return Snapshot(
                    vendor: .kimi,
                    plan: "Kimi Code (\(model))",
                    bar: barWin,
                    windows: [],
                    notes: [note],
                    measuredAt: Date(),
                    balanceAmount: total,
                    balanceCurrency: "CNY",
                    balanceIsLow: isLow
                )
            }
        }

        let w = Window(label: model, short: "k", used: 0, resetsAt: nil)
        return Snapshot(vendor: .kimi, plan: "Kimi Code (\(model))", bar: w, windows: [w],
                        notes: ["Active model: \(model)"], measuredAt: activeTime)
    }

    // MARK: - Reasonix CLI

    static func reasonix() -> Snapshot {
        let root = "\(home)/.reasonix"
        guard FileManager.default.fileExists(atPath: root) else { return .status(.reasonix, .missing) }
        let statsDir = "\(root)/stats"
        var model = "deepseek-v4-pro"
        var todayCost = 0.0
        var latestDate: Date?

        if let files = try? FileManager.default.contentsOfDirectory(atPath: statsDir) {
            let jsonlFiles = files.filter { $0.hasSuffix(".jsonl") }.sorted()
            if let newest = jsonlFiles.last {
                let path = "\(statsDir)/\(newest)"
                latestDate = mtime(path)
                if let tail = try? String(contentsOfFile: path) {
                    for line in tail.split(separator: "\n").suffix(50) {
                        guard let obj = json(Data(line.utf8)) else { continue }
                        if let cost = obj["selected_cost"] as? Double { todayCost += cost }
                        else if let costStr = obj["cost_amount"] as? String, let c = Double(costStr) { todayCost += c }
                        if let m = obj["model"] as? String { model = m.components(separatedBy: "/").last ?? m }
                    }
                }
            }
        }

        let costNote = todayCost > 0 ? String(format: "Session: $%.2f USD · %@", todayCost, model) : "Ready · \(model)"
        let barWin = Window(label: "Spend", short: "$", used: nil, resetsAt: nil)
        return Snapshot(
            vendor: .reasonix,
            plan: "Reasonix (\(model))",
            bar: barWin,
            windows: [],
            notes: [costNote],
            measuredAt: latestDate ?? Date(),
            balanceAmount: todayCost,
            balanceCurrency: "USD",
            balanceIsLow: false
        )
    }

    // MARK: - Muse Code

    static func muse() -> Snapshot {
        let configPath = "\(home)/.config/muse/settings.json"
        let binPath = "\(home)/.local/bin/muse"
        guard FileManager.default.fileExists(atPath: configPath) || FileManager.default.fileExists(atPath: binPath) else {
            return .status(.muse, .missing)
        }
        var provider = "Meta"
        let authPath = "\(home)/.config/muse/auth.json"
        if let d = try? Data(contentsOf: URL(fileURLWithPath: authPath)), let obj = json(d),
           let provs = obj["providers"] as? [String: Any], let first = provs.keys.first {
            provider = first.capitalized
        }
        let activeTime = mtime(authPath) ?? mtime(configPath) ?? Date()
        let barWin = Window(label: "Active", short: "m", used: nil, resetsAt: nil)
        return Snapshot(
            vendor: .muse,
            plan: "Muse Code (\(provider))",
            bar: barWin,
            windows: [],
            notes: ["Provider: \(provider) · Active"],
            measuredAt: activeTime
        )
    }

    // MARK: - Ollama Local Models

    static func isOllamaRunning() -> Bool {
        guard let url = URL(string: "http://127.0.0.1:11434/api/tags") else { return false }
        var req = URLRequest(url: url)
        req.timeoutInterval = 1.0
        var ok = false
        let sem = DispatchSemaphore(value: 0)
        let task = URLSession.shared.dataTask(with: req) { d, resp, _ in
            if let http = resp as? HTTPURLResponse, http.statusCode == 200 { ok = true }
            sem.signal()
        }
        task.resume()
        _ = sem.wait(timeout: .now() + 1.2)
        return ok
    }

    static func ollamaModelsCount() -> Int? {
        guard let url = URL(string: "http://127.0.0.1:11434/api/tags") else { return nil }
        var req = URLRequest(url: url)
        req.timeoutInterval = 1.5
        var resData: Data?
        let sem = DispatchSemaphore(value: 0)
        let task = URLSession.shared.dataTask(with: req) { d, _, _ in
            resData = d
            sem.signal()
        }
        task.resume()
        _ = sem.wait(timeout: .now() + 1.5)
        guard let d = resData, let obj = json(d), let models = obj["models"] as? [[String: Any]] else { return nil }
        return models.count
    }

    static func ollama() -> Snapshot {
        guard let url = URL(string: "http://127.0.0.1:11434/api/ps") else { return .status(.ollama, .missing) }
        var req = URLRequest(url: url)
        req.timeoutInterval = 2.0
        var resData: Data?
        let sem = DispatchSemaphore(value: 0)
        let task = URLSession.shared.dataTask(with: req) { d, _, _ in
            resData = d
            sem.signal()
        }
        task.resume()
        _ = sem.wait(timeout: .now() + 2.0)

        guard let d = resData, let obj = json(d) else {
            if isOllamaRunning() {
                let count = ollamaModelsCount() ?? 0
                let w = Window(label: "Idle", short: "loc", used: 0, resetsAt: nil)
                let note = count > 0 ? "\(count) local model\(count == 1 ? "" : "s") ready · None in VRAM" : "No model currently loaded"
                return Snapshot(vendor: .ollama, plan: "Ollama Ready", bar: w, windows: [w], notes: [note], measuredAt: Date())
            }
            return .status(.ollama, .missing)
        }

        let models = obj["models"] as? [[String: Any]] ?? []
        if let active = models.first, let name = active["name"] as? String {
            let vram = (active["size_vram"] as? Double) ?? 0
            let vramGB = vram / (1024 * 1024 * 1024)
            let w = Window(label: name, short: "loc", used: 100, resetsAt: nil)
            let note = String(format: "%.1f GB VRAM · Local model active", vramGB)
            return Snapshot(vendor: .ollama, plan: "Ollama Local", bar: w, windows: [w], notes: [note], measuredAt: Date())
        }
        let count = ollamaModelsCount() ?? 0
        let w = Window(label: "Idle", short: "loc", used: 0, resetsAt: nil)
        let note = count > 0 ? "\(count) local model\(count == 1 ? "" : "s") ready · None in VRAM" : "No model currently loaded"
        return Snapshot(vendor: .ollama, plan: "Ollama Ready", bar: w, windows: [w], notes: [note], measuredAt: Date())
    }

    static func relativeDay(_ d: Date) -> String {
        let f = DateFormatter(); f.dateFormat = "HH:mm"
        let cal = Calendar.current
        if cal.isDateInToday(d) { return "today " + f.string(from: d) }
        if cal.isDateInTomorrow(d) { return "tomorrow " + f.string(from: d) }
        f.dateFormat = "EEE d MMM"
        return f.string(from: d)
    }
}

enum Paths {
    /// ~/Library/Caches/limitbar — 0700; everything limitbar writes lives here.
    static let cache: String = {
        let d = "\(CLI.home)/Library/Caches/limitbar"
        try? FileManager.default.createDirectory(atPath: d, withIntermediateDirectories: true,
                                                 attributes: [.posixPermissions: 0o700])
        chmod(d, 0o700)
        return d
    }()

    /// Atomic write, readable only by this user.
    static func write(_ data: Data, to path: String) {
        let tmp = path + ".\(getpid())"
        guard FileManager.default.createFile(atPath: tmp, contents: data, attributes: [.posixPermissions: 0o600]) else { return }
        if rename(tmp, path) != 0 { unlink(tmp) }
    }
}

/// The kernel sandbox (Resources/cli.sb, applied with sandbox-exec) every CLI except `claude` runs in.
/// The CLI may start only programs from its own install folder, may read and write nothing in your home
/// folder but its own state folder, and nothing in other users' folders, mounted volumes or shared temp.
enum Sandbox {
    struct Box { let argv: [String]; let binary: String; let env: [String: String] }

    static func real(_ p: String) -> String? {
        guard let rp = realpath(p, nil) else { return nil }
        defer { free(rp) }
        return String(cString: rp)
    }

    /// nil when the profile or sandbox-exec is missing — then the CLI is not run at all.
    static func wrap(_ v: Vendor, _ link: String, _ args: [String]) -> Box? {
        let bundleProfile = Bundle.main.url(forResource: "cli", withExtension: "sb")?.path
        let fallbackProfile = [
            "\(CLI.home)/Applications/LimitBar.app/Contents/Resources/cli.sb",
            "Resources/cli.sb"
        ].first { FileManager.default.fileExists(atPath: $0) }
        guard let profile = bundleProfile ?? fallbackProfile,
              FileManager.default.isExecutableFile(atPath: "/usr/bin/sandbox-exec"),
              let bin = real(link), let tmp = real(CLI.workDir), let home = real(CLI.home) else { return nil }
        let dir = (bin as NSString).deletingLastPathComponent
        let state: String, execDir: String
        switch v {
        case .grok: state = "\(home)/.grok"; execDir = dir
        case .codex:
            state = "\(home)/.codex"
            if bin.contains("ChatGPT.app") {
                execDir = "/Applications/ChatGPT.app"
            } else {
                let pkg = (dir as NSString).deletingLastPathComponent
                let parent = (pkg as NSString).deletingLastPathComponent
                execDir = (parent as NSString).lastPathComponent.hasPrefix("@") ? parent : pkg
            }
        case .gemini: state = "\(home)/.gemini"; execDir = "\(home)/.gemini/antigravity-cli/bin"
        case .claude, .cursor, .copilot, .deepseek, .kimi, .reasonix, .muse, .ollama: return nil
        }
        guard let start = CLI.launch(bin), let interp = start.first else { return nil }
        let none = "/nonexistent-limitbar"
        let agy = v == .gemini
        let argv = ["/usr/bin/sandbox-exec", "-f", profile, "-D", "HOME=\(home)", "-D", "EXEC=\(bin)",
                    "-D", "INTERP=\(interp)", "-D", "EXECDIR=\(execDir)", "-D", "STATE=\(state)", "-D", "PTMP=\(tmp)",
                    "-D", "HELPER=\(agy ? "/usr/bin/security" : none)",
                    "-D", "KEYCHAINS=\(agy ? "\(home)/Library/Keychains" : none)",
                    "-D", "SHAREDTMP=\(agy ? "/private/tmp" : none)",
                    ] + start + args
        return Box(argv: argv, binary: bin, env: ["TMPDIR": tmp + "/"])
    }
}
