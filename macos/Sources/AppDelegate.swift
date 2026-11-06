import Cocoa
import WebKit

final class AppDelegate: NSObject, NSApplicationDelegate {
    let server = PxServer()
    private(set) var baseURL: URL?
    private var windows: [BrowserWindowController] = []
    private var pendingTargets: [String] = []
    private var startError: String?

    func applicationDidFinishLaunching(_ notification: Notification) {
        NSApp.mainMenu = MainMenu.build()
        let wc = newWindow()
        wc.showStatus("Starting pxi…")
        startServer()
    }

    private func startServer() {
        DispatchQueue.global(qos: .userInitiated).async {
            let env = ShellEnv.load()
            let result = self.server.start(env: env)
            DispatchQueue.main.async {
                switch result {
                case .success(let url):
                    self.baseURL = url
                    self.startError = nil
                    if self.windows.isEmpty { _ = self.newWindow() }
                    for w in self.windows where w.webView.url == nil { w.load(url) }
                    self.flushTargets()
                case .failure(let message):
                    self.startError = message
                    for w in self.windows {
                        w.showStatus("pxi did not start.\n\n\(message)", isError: true, logURL: self.server.logURL)
                    }
                }
            }
        }
    }

    // MARK: - Windows

    @discardableResult
    func newWindow(configuration: WKWebViewConfiguration? = nil, tabbedWith parent: NSWindow? = nil) -> BrowserWindowController {
        let wc = BrowserWindowController(app: self, configuration: configuration)
        windows.append(wc)
        if let parent, let w = wc.window {
            parent.addTabbedWindow(w, ordered: .above)
            w.makeKeyAndOrderFront(nil)
        } else {
            wc.showWindow(nil)
            if let last = windows.dropLast().last?.window, let w = wc.window {
                w.setFrameTopLeftPoint(w.cascadeTopLeft(from: NSPoint(x: last.frame.minX, y: last.frame.maxY)))
            }
        }
        if configuration == nil, let baseURL { wc.load(baseURL) }
        return wc
    }

    func windowClosed(_ wc: BrowserWindowController) {
        windows.removeAll { $0 === wc }
    }

    func isPx0(_ url: URL) -> Bool {
        guard let base = baseURL else { return false }
        let local: Set<String> = ["127.0.0.1", "localhost", "::1"]
        return url.scheme == base.scheme && local.contains(url.host ?? "") && (url.port ?? 80) == (base.port ?? 80)
    }

    func applyZoom(_ zoom: CGFloat, except: BrowserWindowController) {
        for w in windows where w !== except { w.webView.pageZoom = zoom }
    }

    private var frontController: BrowserWindowController? {
        windows.first { $0.window?.isKeyWindow == true } ?? windows.last
    }

    // MARK: - Opening folders: File > Open, Finder "Open With", dropping on the Dock icon, `open -a pxi <dir>`

    func application(_ application: NSApplication, open urls: [URL]) {
        pendingTargets += urls.map(\.path)
        flushTargets()
    }

    private func flushTargets() {
        guard baseURL != nil, !pendingTargets.isEmpty else { return }
        let targets = pendingTargets
        pendingTargets = []
        for (i, t) in targets.enumerated() {
            let wc = (i == 0 ? frontController : nil) ?? newWindow()
            // A window still on its first load runs the request once the server is up.
            if wc.webView.isLoading {
                DispatchQueue.main.asyncAfter(deadline: .now() + 0.4) { wc.openTarget(t) }
            } else {
                wc.openTarget(t)
            }
            wc.window?.makeKeyAndOrderFront(nil)
        }
    }

    @objc func openFolder(_ sender: Any?) {
        let panel = NSOpenPanel()
        panel.canChooseDirectories = true
        panel.canChooseFiles = false
        panel.allowsMultipleSelection = false
        panel.prompt = "Open"
        panel.message = "Choose a folder or repository to open in pxi"
        guard panel.runModal() == .OK, let url = panel.url else { return }
        application(NSApp, open: [url])
    }

    @objc func newDocumentWindow(_ sender: Any?) {
        newWindow().window?.makeKeyAndOrderFront(nil)
    }

    @objc func newTab(_ sender: Any?) {
        newWindow(tabbedWith: NSApp.keyWindow)
    }

    @objc func showAbout(_ sender: Any?) {
        let credits = NSAttributedString(
            string: "Review what your coding agents change, across every worktree.\n\nIncludes px0 by Arpit Bhayani, used under the MIT License.",
            attributes: [.font: NSFont.systemFont(ofSize: 11), .foregroundColor: NSColor.secondaryLabelColor])
        NSApp.orderFrontStandardAboutPanel(options: [.credits: credits])
    }

    @objc func showLogFile(_ sender: Any?) {
        NSWorkspace.shared.open(server.logURL)
    }

    // MARK: - Lifecycle

    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
        if !flag {
            let wc = newWindow()
            if let startError { wc.showStatus("pxi did not start.\n\n\(startError)", isError: true, logURL: server.logURL) }
        }
        return true
    }

    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        let open = windows
        if open.isEmpty { return .terminateNow }
        var remaining = open.count
        var dirty = false
        for w in open {
            w.hasUnsavedWork { d in
                dirty = dirty || d
                remaining -= 1
                if remaining == 0 { self.finishTerminate(dirty: dirty) }
            }
        }
        return .terminateLater
    }

    private func finishTerminate(dirty: Bool) {
        guard dirty else { return NSApp.reply(toApplicationShouldTerminate: true) }
        let a = NSAlert()
        a.messageText = "Quit pxi?"
        a.informativeText = "A window has unsaved edits, or an agent is still changing files. Quitting loses the unsaved edits."
        a.addButton(withTitle: "Quit")
        a.addButton(withTitle: "Cancel")
        a.buttons.first?.hasDestructiveAction = true
        NSApp.reply(toApplicationShouldTerminate: a.runModal() == .alertFirstButtonReturn)
    }

    func applicationWillTerminate(_ notification: Notification) {
        server.stop()
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { false }
}
