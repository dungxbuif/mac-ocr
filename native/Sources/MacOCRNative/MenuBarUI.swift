import AppKit
import Combine
import SwiftUI

private extension WorkerController.Status {
    var title: String {
        switch self {
        case .offline: return "Offline"
        case .starting: return "Starting"
        case .online: return "Online"
        case .stopping: return "Stopping"
        case .error: return "Error"
        }
    }

    var color: Color {
        switch self {
        case .online: return .green
        case .starting, .stopping: return .orange
        case .error: return .red
        case .offline: return .secondary
        }
    }
}

public struct ControlPanelView: View {
    @ObservedObject var controller: WorkerController

    public var body: some View {
        ScrollView(.vertical) {
            VStack(alignment: .leading, spacing: 14) {
            HStack(spacing: 10) {
                ZStack {
                    RoundedRectangle(cornerRadius: 9).fill(Color.primary)
                    Image(systemName: "doc.text.viewfinder").foregroundStyle(Color(nsColor: .windowBackgroundColor))
                }
                .frame(width: 36, height: 36)
                VStack(alignment: .leading, spacing: 2) {
                    Text("Mac OCR Native").font(.headline)
                    HStack(spacing: 5) {
                        Circle().fill(controller.status.color).frame(width: 7, height: 7)
                        Text(controller.status.title).font(.caption).foregroundStyle(.secondary)
                    }
                }
                Spacer()
                Toggle("", isOn: Binding(
                    get: { controller.isEnabled },
                    set: { controller.setEnabled($0) }
                ))
                .labelsHidden()
                .toggleStyle(.switch)
                .disabled(controller.toggleDisabled)
            }

            Text(controller.statusDetail)
                .font(.callout)
                .foregroundStyle(controller.status == .error ? .red : .secondary)
                .lineLimit(3)
                .frame(minHeight: 36, alignment: .topLeading)

            VStack(alignment: .leading, spacing: 10) {
                Text("Connection").font(.caption.weight(.semibold)).foregroundStyle(.secondary)
                TextField("Proxy URL", text: Binding(get: { controller.proxyURL }, set: { controller.updateProxyURL($0) }))
                    .textFieldStyle(.roundedBorder)
                    .disabled(controller.configurationLocked)

                HStack {
                    Label(
                        controller.debugLoggingEnabled ? "Debug diagnostics enabled" : "Standard diagnostics",
                        systemImage: controller.debugLoggingEnabled ? "ladybug.fill" : "checkmark.shield"
                    )
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    Spacer()
                    Text("set by environment")
                        .font(.caption2)
                        .foregroundStyle(.tertiary)
                }

                HStack(spacing: 10) {
                    Stepper(value: Binding(get: { controller.port }, set: { controller.updatePort($0) }), in: 1...65_535) {
                        VStack(alignment: .leading, spacing: 1) {
                            Text("Local port").font(.caption2).foregroundStyle(.secondary)
                            Text("\(controller.port)").fontDesign(.monospaced)
                        }
                    }
                    Stepper(value: Binding(get: { controller.operatorLimit }, set: { controller.updateOperatorLimit($0) }), in: 0...256) {
                        VStack(alignment: .leading, spacing: 1) {
                            Text("Concurrency ceiling").font(.caption2).foregroundStyle(.secondary)
                            Text("\(controller.operatorLimit)").fontDesign(.monospaced)
                        }
                    }
                }
                .disabled(controller.configurationLocked)

                VStack(alignment: .leading, spacing: 4) {
                    Text("Connection key (HMAC)").font(.caption2).foregroundStyle(.secondary)
                    SecureField("Enter connection key", text: Binding(get: { controller.authSecret }, set: { controller.updateAuthSecret($0) }))
                        .textFieldStyle(.roundedBorder)
                        .disabled(controller.configurationLocked)
                    Text("Must match proxy NATIVE_AUTH_SECRET")
                        .font(.caption2)
                        .foregroundStyle(.tertiary)
                }

                HStack {
                    Button { controller.testConnection() } label: {
                        if controller.connectionStatus == .testing {
                            ProgressView().controlSize(.small)
                        } else {
                            Label("Test connection", systemImage: "bolt.horizontal.circle")
                        }
                    }
                    .disabled(controller.configurationLocked || controller.connectionStatus == .testing)
                    connectionLabel
                    Spacer()
                }
            }
            .padding(12)
            .background(Color(nsColor: .controlBackgroundColor))
            .clipShape(RoundedRectangle(cornerRadius: 10))

            VStack(spacing: 0) {
                detailRow("Endpoint", value: controller.endpoint)
                Divider()
                detailRow("Capacity", value: capacityText)
            }
            .background(Color(nsColor: .controlBackgroundColor))
            .clipShape(RoundedRectangle(cornerRadius: 10))

            HStack(spacing: 8) {
                Button { controller.showLogs?() } label: {
                    Label("View logs", systemImage: "text.alignleft")
                }
                .buttonStyle(.borderedProminent)
            }

            Divider()

            Toggle("Launch at Login", isOn: Binding(
                get: { controller.launchAtLogin },
                set: { controller.setLaunchAtLogin($0) }
            ))
            .toggleStyle(.switch)

            HStack {
                Text("Logs: ~/Library/Logs/MacOCR")
                    .font(.caption2)
                    .foregroundStyle(.tertiary)
                Spacer()
                Button("Quit") { controller.requestQuit?() }
                    .buttonStyle(.plain)
                    .foregroundStyle(.secondary)
            }
            }
            .padding(18)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .frame(width: 430, height: 600)
    }

    private var capacityText: String {
        guard let capacity = controller.capacity else { return "—" }
        return "\(capacity.active) jobs · \(capacity.availableUnits) units free · adaptive \(capacity.effectiveLimit)/\(capacity.operatorLimit)"
    }

    private func detailRow(_ label: String, value: String) -> some View {
        HStack {
            Text(label).foregroundStyle(.secondary)
            Spacer()
            Text(value).fontDesign(.monospaced).textSelection(.enabled)
        }
        .font(.caption)
        .padding(.horizontal, 12)
        .padding(.vertical, 9)
    }

    @ViewBuilder
    private var connectionLabel: some View {
        switch controller.connectionStatus {
        case .notTested:
            Label("Not tested", systemImage: "circle.dashed").foregroundStyle(.secondary)
        case .testing:
            Text("Checking proxy and key…").foregroundStyle(.secondary)
        case .verified:
            Label("Proxy verified", systemImage: "checkmark.circle.fill").foregroundStyle(.green)
        case .failed:
            Label("Failed", systemImage: "xmark.circle.fill").foregroundStyle(.red)
        }
    }
}

public struct LogWindowView: View {
    @ObservedObject var controller: WorkerController
    @State private var selectedLevel = "ALL"
    @State private var autoScroll = true

    private var visibleEntries: [NativeLogEntry] {
        selectedLevel == "ALL" ? controller.logs : controller.logs.filter { $0.level.rawValue == selectedLevel }
    }

    public var body: some View {
        VStack(spacing: 0) {
            HStack {
                VStack(alignment: .leading, spacing: 2) {
                    Text("Native logs").font(.title3.weight(.semibold))
                    Text(controller.logRecorder.fileURL.path).font(.caption).foregroundStyle(.secondary).lineLimit(1)
                }
                Spacer()
                Picker("Level", selection: $selectedLevel) {
                    Text("All").tag("ALL")
                    ForEach(NativeLogLevel.allCases, id: \.rawValue) { level in Text(level.rawValue).tag(level.rawValue) }
                }
                .labelsHidden()
                .frame(width: 110)
            }
            .padding(14)

            Divider()

            ScrollViewReader { proxy in
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 3) {
                        ForEach(visibleEntries) { entry in
                            HStack(alignment: .firstTextBaseline, spacing: 9) {
                                Text(time(entry.timestamp)).foregroundStyle(.secondary).frame(width: 88, alignment: .leading)
                                Text(entry.level.rawValue)
                                    .foregroundStyle(levelColor(entry.level))
                                    .frame(width: 45, alignment: .leading)
                                Text(entry.message).textSelection(.enabled)
                            }
                            .font(.system(size: 11, design: .monospaced))
                            .id(entry.id)
                        }
                    }
                    .padding(12)
                    .frame(maxWidth: .infinity, alignment: .leading)
                }
                .background(Color(nsColor: .textBackgroundColor))
                .onChange(of: controller.logs.count) { _ in
                    guard autoScroll, let last = visibleEntries.last else { return }
                    withAnimation(.easeOut(duration: 0.15)) { proxy.scrollTo(last.id, anchor: .bottom) }
                }
            }

            Divider()

            HStack {
                Toggle("Auto-scroll", isOn: $autoScroll).toggleStyle(.checkbox)
                Text("\(visibleEntries.count) entries").foregroundStyle(.secondary)
                Spacer()
                Button("Reveal file") { controller.revealLogFile() }
                Button("Copy visible") { controller.copyLogs(visibleEntries) }
                Button("Clear") { controller.clearLogs() }
            }
            .font(.caption)
            .padding(12)
        }
        .frame(minWidth: 760, minHeight: 460)
    }

    private func time(_ date: Date) -> String {
        let formatter = DateFormatter()
        formatter.dateFormat = "HH:mm:ss.SSS"
        return formatter.string(from: date)
    }

    private func levelColor(_ level: NativeLogLevel) -> Color {
        switch level {
        case .debug: return .secondary
        case .info: return .primary
        case .warning: return .orange
        case .error: return .red
        }
    }
}

@MainActor
public final class AppDelegate: NSObject, NSApplicationDelegate, NSPopoverDelegate {
    private let controller = WorkerController()
    private let statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.squareLength)
    private let popover = NSPopover()
    private var logWindow: NSWindow?
    private var cancellables = Set<AnyCancellable>()

    public func applicationDidFinishLaunching(_ notification: Notification) {
        NSApp.setActivationPolicy(.accessory)
        if let button = statusItem.button {
            button.image = NSImage(systemSymbolName: "doc.text.viewfinder", accessibilityDescription: "Mac OCR Native")
            button.action = #selector(togglePopover)
            button.target = self
            button.toolTip = "Mac OCR Native"
        }

        popover.behavior = .transient
        popover.animates = true
        popover.delegate = self
        popover.contentSize = NSSize(width: 430, height: 600)
        popover.contentViewController = NSHostingController(rootView: ControlPanelView(controller: controller))

        controller.showLogs = { [weak self] in self?.showLogWindow() }
        controller.requestQuit = { [weak self] in self?.confirmQuit() }
        controller.$status.sink { [weak self] status in self?.updateStatusIcon(status) }.store(in: &cancellables)
        controller.startOffline()
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.35) { [weak self] in self?.showPopover() }
    }

    public func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { false }

    @objc private func togglePopover() {
        if popover.isShown {
            popover.performClose(nil)
        } else {
            showPopover()
        }
    }

    private func showPopover() {
        guard let button = statusItem.button else { return }
        popover.show(relativeTo: button.bounds, of: button, preferredEdge: .minY)
        NSApp.activate(ignoringOtherApps: true)
    }

    private func showLogWindow() {
        if logWindow == nil {
            let window = NSWindow(
                contentRect: NSRect(x: 0, y: 0, width: 860, height: 540),
                styleMask: [.titled, .closable, .miniaturizable, .resizable],
                backing: .buffered,
                defer: false
            )
            window.title = "Mac OCR Native Logs"
            window.contentViewController = NSHostingController(rootView: LogWindowView(controller: controller))
            window.isReleasedWhenClosed = false
            window.center()
            logWindow = window
        }
        logWindow?.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
    }

    private func updateStatusIcon(_ status: WorkerController.Status) {
        let symbol: String
        switch status {
        case .online: symbol = "doc.text.viewfinder"
        case .starting, .stopping: symbol = "arrow.triangle.2.circlepath"
        case .error: symbol = "exclamationmark.triangle"
        case .offline: symbol = "doc.text.magnifyingglass"
        }
        statusItem.button?.image = NSImage(systemSymbolName: symbol, accessibilityDescription: "Mac OCR Native \(status.rawValue)")
    }

    private func confirmQuit() {
        if controller.activeJobCount() > 0 {
            let alert = NSAlert()
            alert.messageText = "OCR work is still active"
            alert.informativeText = "Quitting now interrupts active OCR jobs. Stop the worker and wait for draining if you want callbacks to complete."
            alert.addButton(withTitle: "Cancel")
            alert.addButton(withTitle: "Quit Anyway")
            if alert.runModal() != .alertSecondButtonReturn { return }
        }
        NSApp.terminate(nil)
    }
}
