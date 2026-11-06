import Cocoa
import WebKit

/// One pxi window: a WKWebView showing the pxi UI, with the browser behaviours it
/// relies on (dialogs, new tabs, leave-page warnings) done natively.
final class BrowserWindowController: NSWindowController, NSWindowDelegate, WKNavigationDelegate, WKUIDelegate {
    let webView: WKWebView
    private let loading = LoadingView()
    private var titleObservation: NSKeyValueObservation?
    private var closeConfirmed = false
    weak var app: AppDelegate?

    /// - configuration: WebKit's own, when it asks for a window (window.open).
    init(app: AppDelegate, configuration: WKWebViewConfiguration? = nil) {
        self.app = app
        let config = configuration ?? BrowserWindowController.baseConfiguration()
        webView = WKWebView(frame: .zero, configuration: config)
        webView.setValue(false, forKey: "drawsBackground") // no white flash in dark themes
        if #available(macOS 13.3, *) { webView.isInspectable = true }
        webView.allowsBackForwardNavigationGestures = true
        webView.allowsMagnification = true
        webView.pageZoom = CGFloat(UserDefaults.standard.double(forKey: "pageZoom").nonZero ?? 1)

        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 1440, height: 920),
                              styleMask: [.titled, .closable, .miniaturizable, .resizable, .fullSizeContentView],
                              backing: .buffered, defer: false)
        window.title = "pxi"
        window.titlebarAppearsTransparent = false
        window.tabbingMode = .preferred
        window.backgroundColor = .windowBackgroundColor
        window.minSize = NSSize(width: 640, height: 400)
        super.init(window: window)

        window.delegate = self
        webView.navigationDelegate = self
        webView.uiDelegate = self

        let content = NSView()
        window.contentView = content
        for v in [webView, loading] as [NSView] {
            v.translatesAutoresizingMaskIntoConstraints = false
            content.addSubview(v)
            NSLayoutConstraint.activate([
                v.leadingAnchor.constraint(equalTo: content.leadingAnchor),
                v.trailingAnchor.constraint(equalTo: content.trailingAnchor),
                v.topAnchor.constraint(equalTo: content.safeAreaLayoutGuide.topAnchor),
                v.bottomAnchor.constraint(equalTo: content.bottomAnchor),
            ])
        }

        titleObservation = webView.observe(\.title, options: [.new]) { [weak self] wv, _ in
            let t = (wv.title ?? "").trimmingCharacters(in: .whitespaces)
            self?.window?.title = t.isEmpty ? "pxi" : t
        }
        if !window.setFrameUsingName("pxi.main") { window.center() }
        window.setFrameAutosaveName("pxi.main")
    }

    required init?(coder: NSCoder) { fatalError("unused") }

    static func baseConfiguration() -> WKWebViewConfiguration {
        let c = WKWebViewConfiguration()
        c.websiteDataStore = .default() // keeps the page's localStorage across launches
        c.preferences.isElementFullscreenEnabled = true
        c.preferences.setValue(true, forKey: "developerExtrasEnabled")
        return c
    }

    // MARK: - Loading

    func load(_ url: URL) {
        loading.show(message: nil)
        webView.load(URLRequest(url: url))
    }

    func showStatus(_ message: String, isError: Bool = false, logURL: URL? = nil) {
        loading.show(message: message, isError: isError, logURL: logURL)
    }

    func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
        loading.hide()
        snapshotForDebugging()
    }

    /// PXI_SNAPSHOT=<dir>: save a PNG of each loaded page there, for checking
    /// the app from scripts without screen-recording permission.
    private func snapshotForDebugging() {
        guard let dir = ProcessInfo.processInfo.environment["PXI_SNAPSHOT"] else { return }
        DispatchQueue.main.asyncAfter(deadline: .now() + 2.5) { [weak self] in
            self?.webView.takeSnapshot(with: nil) { image, _ in
                guard let image, let tiff = image.tiffRepresentation,
                      let png = NSBitmapImageRep(data: tiff)?.representation(using: .png, properties: [:]) else { return }
                let name = String(format: "snap-%.0f.png", Date().timeIntervalSince1970 * 1000)
                try? png.write(to: URL(fileURLWithPath: dir).appendingPathComponent(name))
            }
        }
    }

    func webView(_ webView: WKWebView, didFail navigation: WKNavigation!, withError error: Error) {
        loadFailed(error)
    }

    func webView(_ webView: WKWebView, didFailProvisionalNavigation navigation: WKNavigation!, withError error: Error) {
        loadFailed(error)
    }

    private func loadFailed(_ error: Error) {
        let ns = error as NSError
        if ns.domain == NSURLErrorDomain && ns.code == NSURLErrorCancelled { return }
        loading.show(message: "Could not load pxi: \(error.localizedDescription)", isError: true, logURL: app?.server.logURL)
    }

    func webViewWebContentProcessDidTerminate(_ webView: WKWebView) {
        webView.reload() // the page's process crashed or was reclaimed
    }

    // MARK: - Navigation policy: pxi pages stay in pxi, everything else opens in the browser

    func webView(_ webView: WKWebView, decidePolicyFor action: WKNavigationAction,
                 decisionHandler: @escaping (WKNavigationActionPolicy) -> Void) {
        guard let url = action.request.url else { return decisionHandler(.allow) }
        let isMain = action.targetFrame?.isMainFrame ?? true
        if !isMain || ["about", "blob", "data"].contains(url.scheme ?? "") || app?.isPx0(url) == true {
            return decisionHandler(.allow)
        }
        NSWorkspace.shared.open(url)
        decisionHandler(.cancel)
        // A window opened only to show an outside link has nothing else to show.
        if webView.url == nil || webView.url?.absoluteString == "about:blank" {
            window?.close()
        }
    }

    func webView(_ webView: WKWebView, createWebViewWith configuration: WKWebViewConfiguration,
                 for action: WKNavigationAction, windowFeatures: WKWindowFeatures) -> WKWebView? {
        if let url = action.request.url, url.scheme?.hasPrefix("http") == true, app?.isPx0(url) == false {
            NSWorkspace.shared.open(url)
            return nil
        }
        // pxi's own pages (a PR review, a workspace opened in a "new tab")
        // open as a new pxi window, in the same window group as this one.
        guard let app else { return nil }
        let wc = app.newWindow(configuration: configuration, tabbedWith: window)
        return wc.webView
    }

    func webViewDidClose(_ webView: WKWebView) {
        closeConfirmed = true
        window?.close()
    }

    // MARK: - JavaScript dialogs (WKWebView shows none unless asked)

    func webView(_ webView: WKWebView, runJavaScriptAlertPanelWithMessage message: String,
                 initiatedByFrame frame: WKFrameInfo, completionHandler: @escaping () -> Void) {
        let a = NSAlert()
        a.messageText = message
        a.addButton(withTitle: "OK")
        present(a) { _ in completionHandler() }
    }

    func webView(_ webView: WKWebView, runJavaScriptConfirmPanelWithMessage message: String,
                 initiatedByFrame frame: WKFrameInfo, completionHandler: @escaping (Bool) -> Void) {
        let a = NSAlert()
        a.messageText = message
        a.addButton(withTitle: "OK")
        a.addButton(withTitle: "Cancel")
        present(a) { completionHandler($0 == .alertFirstButtonReturn) }
    }

    func webView(_ webView: WKWebView, runJavaScriptTextInputPanelWithPrompt prompt: String, defaultText: String?,
                 initiatedByFrame frame: WKFrameInfo, completionHandler: @escaping (String?) -> Void) {
        let a = NSAlert()
        a.messageText = prompt
        let field = NSTextField(frame: NSRect(x: 0, y: 0, width: 320, height: 24))
        field.stringValue = defaultText ?? ""
        a.accessoryView = field
        a.addButton(withTitle: "OK")
        a.addButton(withTitle: "Cancel")
        a.window.initialFirstResponder = field
        present(a) { completionHandler($0 == .alertFirstButtonReturn ? field.stringValue : nil) }
    }

    func webView(_ webView: WKWebView, runOpenPanelWith parameters: WKOpenPanelParameters,
                 initiatedByFrame frame: WKFrameInfo, completionHandler: @escaping ([URL]?) -> Void) {
        let panel = NSOpenPanel()
        panel.allowsMultipleSelection = parameters.allowsMultipleSelection
        panel.canChooseDirectories = parameters.allowsDirectories
        panel.canChooseFiles = true
        guard let window else { return completionHandler(nil) }
        panel.beginSheetModal(for: window) { completionHandler($0 == .OK ? panel.urls : nil) }
    }

    private func present(_ alert: NSAlert, _ done: @escaping (NSApplication.ModalResponse) -> Void) {
        if let window, window.isVisible {
            alert.beginSheetModal(for: window, completionHandler: done)
        } else {
            done(alert.runModal())
        }
    }

    // MARK: - Closing: the page's "leave page?" checks (unsaved edits, agents still writing)

    /// Asks the page whether leaving would lose work, the way a browser does on
    /// close: the page marks that by cancelling `beforeunload`.
    func hasUnsavedWork(_ done: @escaping (Bool) -> Void) {
        let js = "const e = new Event('beforeunload', {cancelable: true}); window.dispatchEvent(e); return e.defaultPrevented;"
        webView.callAsyncJavaScript(js, arguments: [:], in: nil, in: .page) { result in
            if case .success(let v) = result { done((v as? Bool) == true) } else { done(false) }
        }
    }

    func windowShouldClose(_ sender: NSWindow) -> Bool {
        if closeConfirmed { return true }
        hasUnsavedWork { [weak self] dirty in
            guard let self else { return }
            if !dirty {
                self.closeConfirmed = true
                self.window?.close()
                return
            }
            let a = NSAlert()
            a.messageText = "Close this window?"
            a.informativeText = "It has unsaved edits, or an agent is still changing files. Closing loses the unsaved edits."
            a.addButton(withTitle: "Close")
            a.addButton(withTitle: "Cancel")
            a.buttons.first?.hasDestructiveAction = true
            self.present(a) { r in
                if r == .alertFirstButtonReturn {
                    self.closeConfirmed = true
                    self.window?.close()
                }
            }
        }
        return false
    }

    func windowWillClose(_ notification: Notification) {
        app?.windowClosed(self)
    }

    // MARK: - Menu actions (reached through the responder chain)

    @objc func reloadPage(_ sender: Any?) { webView.reload() }
    @objc func goBack(_ sender: Any?) { webView.goBack() }
    @objc func goForward(_ sender: Any?) { webView.goForward() }

    @objc func showWorkbench(_ sender: Any?) {
        if let url = app?.baseURL { load(url) }
    }

    @objc func zoomIn(_ sender: Any?) { setZoom(webView.pageZoom * 1.1) }
    @objc func zoomOut(_ sender: Any?) { setZoom(webView.pageZoom / 1.1) }
    @objc func actualSize(_ sender: Any?) { setZoom(1) }

    private func setZoom(_ z: CGFloat) {
        let clamped = min(3, max(0.5, z))
        webView.pageZoom = clamped
        UserDefaults.standard.set(Double(clamped), forKey: "pageZoom")
        app?.applyZoom(clamped, except: self)
    }

    /// Opens a folder (or PR URL) as a pxi workspace in this window, through the
    /// workbench's own API so it lands in Recent like any other.
    func openTarget(_ target: String) {
        let js = """
        const r = await fetch(new URL('/api/wb/open', location.origin), {
          method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({target})
        });
        const j = await r.json();
        if (j.error) return 'error:' + j.error;
        if (j.needClone) { location.href = new URL('/', location.origin).href; return 'needClone'; }
        location.href = new URL(j.href, location.origin).href;
        return 'ok';
        """
        webView.callAsyncJavaScript(js, arguments: ["target": target], in: nil, in: .page) { [weak self] result in
            var message: String?
            switch result {
            case .success(let v): if let s = v as? String, s.hasPrefix("error:") { message = String(s.dropFirst(6)) }
            case .failure(let e): message = e.localizedDescription
            }
            if let message {
                let a = NSAlert()
                a.messageText = "Could not open \(target)"
                a.informativeText = message
                self?.present(a) { _ in }
            }
        }
    }
}

private extension Double {
    var nonZero: Double? { self == 0 ? nil : self }
}

/// Shown until the first page loads, and for startup errors.
final class LoadingView: NSView {
    private let spinner = NSProgressIndicator()
    private let label = NSTextField(wrappingLabelWithString: "")
    private let logButton = NSButton(title: "Show Log", target: nil, action: nil)
    private var logURL: URL?

    override init(frame: NSRect) {
        super.init(frame: frame)
        wantsLayer = true
        layer?.backgroundColor = NSColor.windowBackgroundColor.cgColor
        spinner.style = .spinning
        spinner.controlSize = .regular
        label.alignment = .center
        label.textColor = .secondaryLabelColor
        label.maximumNumberOfLines = 0
        logButton.target = self
        logButton.action = #selector(showLog)
        logButton.isHidden = true
        let stack = NSStackView(views: [spinner, label, logButton])
        stack.orientation = .vertical
        stack.spacing = 12
        stack.translatesAutoresizingMaskIntoConstraints = false
        addSubview(stack)
        NSLayoutConstraint.activate([
            stack.centerXAnchor.constraint(equalTo: centerXAnchor),
            stack.centerYAnchor.constraint(equalTo: centerYAnchor),
            stack.widthAnchor.constraint(lessThanOrEqualToConstant: 560),
        ])
    }

    required init?(coder: NSCoder) { fatalError("unused") }

    override func updateLayer() {
        layer?.backgroundColor = NSColor.windowBackgroundColor.cgColor
    }

    func show(message: String?, isError: Bool = false, logURL: URL? = nil) {
        isHidden = false
        label.stringValue = message ?? ""
        label.textColor = isError ? .systemRed : .secondaryLabelColor
        spinner.isHidden = isError
        if isError { spinner.stopAnimation(nil) } else { spinner.startAnimation(nil) }
        self.logURL = logURL
        logButton.isHidden = logURL == nil
    }

    func hide() {
        spinner.stopAnimation(nil)
        isHidden = true
    }

    @objc private func showLog() {
        if let logURL { NSWorkspace.shared.open(logURL) }
    }
}
