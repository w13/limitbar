import AppKit
import ServiceManagement
import SwiftUI

enum Marks {
    private static var cache: [String: NSImage] = [:]

    static func image(_ v: Vendor) -> NSImage {
        if let i = cache[v.mark] { return i }
        let url = Bundle.main.resourceURL?.appendingPathComponent("marks/\(v.mark).svg")
        let img = url.flatMap { NSImage(contentsOf: $0) } ?? NSImage()
        img.size = NSSize(width: 16, height: 16)
        img.isTemplate = true
        cache[v.mark] = img
        return img
    }
}

extension Color {
    static let claude = Color(red: 0xD9 / 255, green: 0x77 / 255, blue: 0x57 / 255)
    static let hot = Color(nsColor: .systemRed)
    static let geminiGradient = LinearGradient(
        colors: [Color(red: 0x3D / 255, green: 0x8B / 255, blue: 1),
                 Color(red: 0xB0 / 255, green: 0x6B / 255, blue: 0xDB / 255)],
        startPoint: .leading, endPoint: .trailing
    )
    static let codexEmerald = Color(red: 0x10 / 255, green: 0xA3 / 255, blue: 0x7F / 255)
    static let cursorCyan = Color(red: 0x00 / 255, green: 0xA3 / 255, blue: 0xFF / 255)
    static let copilotPurple = Color(red: 0x6E / 255, green: 0x40 / 255, blue: 0xC9 / 255)
    static let deepseekBlue = Color(red: 0x4D / 255, green: 0x6B / 255, blue: 0xFE / 255)
    static let kimiCrimson = Color(red: 0xFF / 255, green: 0x2A / 255, blue: 0x6D / 255)
    static let reasonixPurple = Color(red: 0x8B / 255, green: 0x5C / 255, blue: 0xF6 / 255)
    static let museAmber = Color(red: 0xF5 / 255, green: 0x9E / 255, blue: 0x0B / 255)
}

private let staleAfter: TimeInterval = 45 * 60

private func isStale(_ s: Snapshot?) -> Bool {
    guard let m = s?.measuredAt else { return true }
    return Date().timeIntervalSince(m) > staleAfter
}

// MARK: - Tray (Menu Bar Item)

struct TrayView: View {
    @ObservedObject var store: Store

    var body: some View {
        HStack(spacing: 8) {
            ForEach(store.installed, id: \.self) { v in
                let s = store.snaps[v]
                HStack(spacing: 3) {
                    Image(nsImage: Marks.image(v))
                        .renderingMode(.template)
                        .resizable()
                        .interpolation(.high)
                        .frame(width: 13, height: 13)
                    digit(s)
                }
            }
        }
        .padding(.horizontal, 7)
        .frame(height: 22)
        .fixedSize()
    }

    private func digit(_ s: Snapshot?) -> some View {
        let w = s?.state == .ok ? s?.bar : nil
        let shown: Double? = (w == nil || w!.resetPassed) ? nil : w!.used
        let crit = (shown ?? 0) >= 90
        let weekly = w?.short == "w"
        let bang = s?.otherHot ?? false
        var t = Text(percentText(shown))
            .font(.system(size: 13, weight: crit ? .semibold : .regular).monospacedDigit())
        if weekly || bang {
            var sup = Text(weekly ? "w" : "").font(.system(size: 8.5, weight: .medium))
            if bang { sup = sup + Text("!").font(.system(size: 8.5, weight: .heavy)) }
            t = t + sup.baselineOffset(5)
        }
        return t.foregroundStyle(crit ? Color.hot : Color.primary)
            .opacity(isStale(s) && shown != nil ? 0.5 : 1)
            .fixedSize()
    }
}

// MARK: - Panel (Scrollable 2-Column Grid)

final class PanelUIState: ObservableObject {
    @Published var login = SMAppService.mainApp.status == .enabled
}

struct PanelView: View {
    @ObservedObject var store: Store
    var onQuit: () -> Void
    @ObservedObject private var ui = PanelUIState()

    private let columns = [
        GridItem(.flexible(), spacing: 8),
        GridItem(.flexible(), spacing: 8)
    ]

    var body: some View {
        TimelineView(.periodic(from: .now, by: 30)) { _ in
            VStack(spacing: 0) {
                // Header info
                headerBar

                Hairline()

                // Scrollable 2-column card grid
                ScrollView(.vertical, showsIndicators: true) {
                    VStack(spacing: 8) {
                        LazyVGrid(columns: columns, spacing: 8) {
                            ForEach(store.installed, id: \.self) { v in
                                VendorCard(
                                    vendor: v,
                                    snap: store.snaps[v],
                                    busy: store.busy.contains(v),
                                    onConfigure: { promptConfig(for: v) }
                                )
                            }
                        }

                        let missing = Vendor.allCases.filter { !store.installed.contains($0) }
                        if !missing.isEmpty {
                            MissingRow(vendors: missing, onConfigure: { v in
                                promptConfig(for: v)
                            })
                        }
                    }
                    .padding(8)
                }
                .frame(maxHeight: 460)

                // Fixed footer (always visible)
                Hairline()
                footer
            }
            .frame(width: 550)
        }
    }

    private var headerBar: some View {
        HStack(spacing: 6) {
            Text("AI Subscriptions & Limits")
                .font(.system(size: 12, weight: .semibold))
            Text("(\(store.installed.count) active)")
                .font(.system(size: 11))
                .foregroundStyle(.secondary)
            Spacer()
            if !store.busy.isEmpty {
                Text("Refreshing…")
                    .font(.system(size: 11))
                    .foregroundStyle(.tertiary)
            }
        }
        .padding(EdgeInsets(top: 8, leading: 12, bottom: 7, trailing: 12))
    }

    private var footer: some View {
        HStack(spacing: 6) {
            FooterButton(title: store.busy.isEmpty ? "Refresh" : "Refreshing…") { store.refresh(force: true) }
                .disabled(!store.busy.isEmpty)
            Spacer()
            HStack(spacing: 6) {
                Text("Launch at Login").font(.system(size: 11.5))
                Toggle("", isOn: Binding(get: { ui.login }, set: { on in
                    do {
                        if on { try SMAppService.mainApp.register() } else { try SMAppService.mainApp.unregister() }
                    } catch {}
                    ui.login = SMAppService.mainApp.status == .enabled
                })).toggleStyle(.switch).controlSize(.mini).labelsHidden()
            }
            Spacer()
            FooterButton(title: "Quit", action: onQuit)
        }
        .padding(EdgeInsets(top: 6, leading: 10, bottom: 8, trailing: 10))
    }

    private func promptConfig(for vendor: Vendor) {
        if vendor == .deepseek {
            let alert = NSAlert()
            alert.messageText = "Configure DeepSeek API Key"
            alert.informativeText = "Enter your DeepSeek API key (sk-...) to monitor your account balance:"
            alert.alertStyle = .informational
            let input = NSSecureTextField(frame: NSRect(x: 0, y: 0, width: 280, height: 24))
            input.placeholderString = "sk-..."
            if let current = Fetch.deepseekKey() { input.stringValue = current }
            alert.accessoryView = input
            alert.addButton(withTitle: "Save")
            alert.addButton(withTitle: "Cancel")
            if alert.runModal() == .alertFirstButtonReturn {
                let key = input.stringValue.trimmingCharacters(in: .whitespacesAndNewlines)
                if !key.isEmpty {
                    UserDefaults.standard.set(key, forKey: "deepseekKey")
                    store.refresh(force: true)
                }
            }
        }
    }
}

private struct Hairline: View {
    var body: some View { Rectangle().fill(Color.primary.opacity(0.12)).frame(height: 0.5) }
}

private struct FooterButton: View {
    let title: String
    let action: () -> Void
    var body: some View {
        Button(action: action) {
            Text(title).font(.system(size: 11.5)).padding(.horizontal, 8).padding(.vertical, 3.5)
                .background(RoundedRectangle(cornerRadius: 6).fill(Color.primary.opacity(0.08)))
        }
        .buttonStyle(.plain)
    }
}

// MARK: - Vendor Card (Compact Card in 2-Column Grid)

private struct VendorCard: View {
    let vendor: Vendor
    let snap: Snapshot?
    let busy: Bool
    var onConfigure: (() -> Void)?

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            // Card Header
            HStack(alignment: .center, spacing: 6) {
                markView.frame(width: 15, height: 15)
                HStack(spacing: 3) {
                    Text(vendor.name)
                        .font(.system(size: 12, weight: .semibold))
                        .tracking(-0.1)
                        .lineLimit(1)
                    if vendor.appBundleId != nil && hasAppInstalled(vendor) {
                        Button(action: { openApp(for: vendor) }) {
                            Image(systemName: "arrow.up.forward.app")
                                .font(.system(size: 9))
                                .foregroundStyle(.tertiary)
                        }
                        .buttonStyle(.plain)
                        .help("Launch or focus \(vendor.name)")
                    }
                }
                Spacer(minLength: 4)
                Text(ageText)
                    .font(.system(size: 9.5))
                    .foregroundStyle(.tertiary)
            }

            // Plan Subtitle
            Text(planLine)
                .font(.system(size: 10))
                .foregroundStyle(.secondary)
                .lineLimit(1)
                .truncationMode(.tail)

            // Limit Windows / Meters
            let windows = snap?.windows ?? []
            ForEach(Array(windows.enumerated()), id: \.offset) { i, w in
                MeterRow(window: w, vendor: vendor,
                         style: i == 0 ? .primary : .normal)
                    .opacity(isStale(snap) ? 0.48 : 1)
            }

            // Status message / fallback
            if snap?.state == .signedOut {
                Text("Signed out")
                    .font(.system(size: 10.5))
                    .foregroundStyle(.secondary)
            } else if windows.isEmpty {
                Text(busy ? "Reading…" : "—")
                    .font(.system(size: 10.5))
                    .foregroundStyle(.secondary)
            }

            // Footnotes (spend, requests, notes)
            ForEach(snap?.notes ?? [], id: \.self) { n in
                Text(n)
                    .font(.system(size: 9.5))
                    .foregroundStyle(.tertiary)
                    .lineLimit(1)
                    .truncationMode(.tail)
            }

            if let e = snap?.error {
                HStack(spacing: 3) {
                    Text(e)
                        .font(.system(size: 9.5))
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                    if vendor == .deepseek {
                        Button(action: { onConfigure?() }) {
                            Text("Set Key")
                                .font(.system(size: 9.5, weight: .semibold))
                                .foregroundStyle(Color.deepseekBlue)
                        }
                        .buttonStyle(.plain)
                    }
                }
            }
        }
        .padding(EdgeInsets(top: 7, leading: 9, bottom: 7, trailing: 9))
        .background(
            RoundedRectangle(cornerRadius: 8)
                .fill(Color.primary.opacity(0.04))
                .overlay(RoundedRectangle(cornerRadius: 8).stroke(Color.primary.opacity(0.08), lineWidth: 0.5))
        )
    }

    private func hasAppInstalled(_ v: Vendor) -> Bool {
        guard let b = v.appBundleId else { return false }
        return NSWorkspace.shared.urlForApplication(withBundleIdentifier: b) != nil
    }

    private func openApp(for v: Vendor) {
        if let b = v.appBundleId, let url = NSWorkspace.shared.urlForApplication(withBundleIdentifier: b) {
            NSWorkspace.shared.openApplication(at: url, configuration: NSWorkspace.OpenConfiguration())
        }
    }

    @ViewBuilder private var markView: some View {
        let img = Image(nsImage: Marks.image(vendor)).renderingMode(.template).resizable().interpolation(.high)
            .frame(width: 14, height: 14)
        switch vendor {
        case .claude: img.foregroundStyle(Color.claude)
        case .gemini: img.foregroundStyle(Color.geminiGradient)
        case .codex: img.foregroundStyle(Color.codexEmerald)
        case .grok: img.foregroundStyle(Color.primary)
        case .cursor: img.foregroundStyle(Color.cursorCyan)
        case .copilot: img.foregroundStyle(Color.copilotPurple)
        case .deepseek: img.foregroundStyle(Color.deepseekBlue)
        case .kimi: img.foregroundStyle(Color.kimiCrimson)
        case .reasonix: img.foregroundStyle(Color.reasonixPurple)
        case .muse: img.foregroundStyle(Color.museAmber)
        case .ollama: img.foregroundStyle(Color.primary)
        }
    }

    private var planLine: String {
        let plan = snap?.plan ?? ""
        return plan.isEmpty ? vendor.product : plan
    }

    private var ageText: String {
        if busy && snap?.measuredAt == nil { return "reading…" }
        guard let m = snap?.measuredAt else { return "" }
        let s = Int(Date().timeIntervalSince(m))
        return s < 60 ? "now" : s < 3600 ? "\(s / 60)m ago" : s < 86400 ? "\(s / 3600)h ago" : "\(s / 86400)d ago"
    }
}

private struct MissingRow: View {
    let vendors: [Vendor]
    var onConfigure: ((Vendor) -> Void)?

    var body: some View {
        VStack(alignment: .leading, spacing: 3) {
            Text("Other Providers")
                .font(.system(size: 10.5, weight: .medium))
                .foregroundStyle(.secondary)
            HStack(spacing: 8) {
                ForEach(vendors, id: \.self) { v in
                    HStack(spacing: 3) {
                        Image(nsImage: Marks.image(v))
                            .renderingMode(.template)
                            .resizable()
                            .frame(width: 11, height: 11)
                            .foregroundStyle(.tertiary)
                        Text(v.name)
                            .font(.system(size: 10))
                            .foregroundStyle(.tertiary)
                    }
                }
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(EdgeInsets(top: 4, leading: 6, bottom: 4, trailing: 6))
    }
}

private struct MeterRow: View {
    enum Style { case primary, normal }
    let window: Window
    let vendor: Vendor
    let style: Style

    var body: some View {
        let due = window.resetPassed
        let hot = !due && (window.used ?? 0) >= 90
        VStack(alignment: .leading, spacing: 2) {
            HStack(alignment: .center, spacing: 4) {
                Text(window.label)
                    .lineLimit(1)
                    .font(.system(size: 10.5, weight: style == .primary ? .medium : .regular))
                    .foregroundStyle(Color.primary.opacity(0.85))
                Spacer()
                Text(due || window.used == nil ? "—" : percentText(window.used) + "%")
                    .font(.system(size: 10.5, weight: hot ? .bold : .semibold).monospacedDigit())
                    .foregroundStyle(hot ? Color.hot : Color.primary)
            }
            if !due {
                Track(fraction: (window.used ?? 0) / 100, height: 3.5, vendor: vendor, hot: hot)
            }
            if let r = resetText(due), !r.isEmpty {
                Text(r)
                    .font(.system(size: 9))
                    .foregroundStyle(due ? .secondary : .tertiary)
                    .lineLimit(1)
            }
        }
    }

    private func resetText(_ due: Bool) -> String? {
        if due { return "reset · awaiting update" }
        guard let r = window.resetsAt else { return nil }
        let f = DateFormatter()
        f.dateFormat = Calendar.current.isDateInToday(r) ? "HH:mm" : "EEE HH:mm"
        return "resets " + f.string(from: r)
    }
}

private struct Track: View {
    let fraction: Double
    let height: CGFloat
    let vendor: Vendor
    let hot: Bool

    var body: some View {
        GeometryReader { g in
            ZStack(alignment: .leading) {
                Capsule().fill(Color.primary.opacity(0.12))
                fill.frame(width: max(fraction > 0 ? height : 0, g.size.width * min(1, max(0, fraction))))
            }
        }
        .frame(height: height)
    }

    @ViewBuilder private var fill: some View {
        if hot { Capsule().fill(Color.hot) }
        else {
            switch vendor {
            case .claude: Capsule().fill(Color.claude)
            case .gemini: Capsule().fill(Color.geminiGradient)
            case .codex: Capsule().fill(Color.codexEmerald)
            case .grok: Capsule().fill(Color.primary)
            case .cursor: Capsule().fill(Color.cursorCyan)
            case .copilot: Capsule().fill(Color.copilotPurple)
            case .deepseek: Capsule().fill(Color.deepseekBlue)
            case .kimi: Capsule().fill(Color.kimiCrimson)
            case .reasonix: Capsule().fill(Color.reasonixPurple)
            case .muse: Capsule().fill(Color.museAmber)
            case .ollama: Capsule().fill(Color.primary)
            }
        }
    }
}
