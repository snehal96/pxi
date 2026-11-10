import Cocoa
import WebKit

/// VS Code-style title bar, drawn natively so it costs the page nothing and the
/// window stays draggable: back/forward through px0's navigation history and a
/// centered search box (the "command center") that opens quick open. It takes
/// the theme's colours from the page (web/src/pxi-native.js).
final class TitleBar: NSObject, NSToolbarDelegate {
    static let nav = NSToolbarItem.Identifier("pxi.nav")
    static let search = NSToolbarItem.Identifier("pxi.search")

    weak var owner: BrowserWindowController?
    private let searchButton = NSButton(title: "", target: nil, action: nil)
    private var name = ""
    private var dim = NSColor.secondaryLabelColor
    private var field = NSColor.controlBackgroundColor

    init(owner: BrowserWindowController) {
        self.owner = owner
        super.init()
        searchButton.target = self
        searchButton.action = #selector(searchClicked)
        searchButton.isBordered = false
        searchButton.wantsLayer = true
        searchButton.layer?.cornerRadius = 6
        searchButton.layer?.borderWidth = 1
        searchButton.toolTip = "Go to file (⌘P)"
        searchButton.translatesAutoresizingMaskIntoConstraints = false
        NSLayoutConstraint.activate([
            searchButton.widthAnchor.constraint(equalToConstant: 460),
            searchButton.heightAnchor.constraint(equalToConstant: 24),
        ])
        drawSearch()
    }

    func install(on window: NSWindow) {
        let tb = NSToolbar(identifier: "pxi.titlebar")
        tb.delegate = self
        tb.displayMode = .iconOnly
        tb.allowsUserCustomization = false
        tb.autosavesConfiguration = false
        if #available(macOS 13.0, *) { tb.centeredItemIdentifiers = [Self.search] }
        window.titleVisibility = .hidden
        window.titlebarAppearsTransparent = true
        window.toolbarStyle = .unified
        window.titlebarSeparatorStyle = .line
        window.toolbar = tb
    }

    // MARK: - Toolbar

    func toolbarDefaultItemIdentifiers(_ toolbar: NSToolbar) -> [NSToolbarItem.Identifier] {
        [Self.nav, .flexibleSpace, Self.search, .flexibleSpace]
    }

    func toolbarAllowedItemIdentifiers(_ toolbar: NSToolbar) -> [NSToolbarItem.Identifier] {
        toolbarDefaultItemIdentifiers(toolbar)
    }

    func toolbar(_ toolbar: NSToolbar, itemForItemIdentifier id: NSToolbarItem.Identifier,
                 willBeInsertedIntoToolbar flag: Bool) -> NSToolbarItem? {
        switch id {
        case Self.nav:
            let images = ["chevron.left", "chevron.right"].compactMap { NSImage(systemSymbolName: $0, accessibilityDescription: nil) }
            let group = NSToolbarItemGroup(itemIdentifier: id, images: images, selectionMode: .momentary,
                                           labels: ["Back", "Forward"], target: self, action: #selector(navClicked(_:)))
            group.controlRepresentation = .expanded
            group.subitems.first?.toolTip = "Go back (⌘[)"
            group.subitems.last?.toolTip = "Go forward (⌘])"
            return group
        case Self.search:
            let item = NSToolbarItem(itemIdentifier: id)
            item.view = searchButton
            item.label = "Search"
            return item
        default:
            return nil
        }
    }

    // MARK: - Actions

    @objc private func navClicked(_ sender: NSToolbarItemGroup) {
        let back = sender.selectedIndex == 0
        let fn = back ? "back" : "forward"
        // In a workspace, px0's navigation history; elsewhere (the workbench), the page's own.
        owner?.webView.evaluateJavaScript("window.pxiNative ? window.pxiNative.\(fn)() : history.\(back ? "back" : "forward")()")
    }

    @objc private func searchClicked() {
        owner?.webView.evaluateJavaScript(
            "window.pxiNative ? window.pxiNative.quickOpen() : document.querySelector('input')?.focus()")
    }

    // MARK: - Look

    func setName(_ name: String) {
        guard name != self.name else { return }
        self.name = name
        drawSearch()
    }

    /// From the page: {bar, fg, dim, field, scheme, name}.
    func applyTheme(_ d: [String: Any], to window: NSWindow?) {
        guard let window else { return }
        if let bar = (d["bar"] as? String).flatMap(NSColor.init(css:)) { window.backgroundColor = bar }
        if let c = (d["dim"] as? String).flatMap(NSColor.init(css:)) { dim = c }
        if let c = (d["field"] as? String).flatMap(NSColor.init(css:)) { field = c }
        let dark = (d["scheme"] as? String ?? "dark").contains("dark")
        window.appearance = NSAppearance(named: dark ? .darkAqua : .aqua)
        if let n = d["name"] as? String, !n.isEmpty { name = n }
        drawSearch()
    }

    private func drawSearch() {
        searchButton.layer?.backgroundColor = field.cgColor
        searchButton.layer?.borderColor = dim.withAlphaComponent(0.35).cgColor
        let text = NSMutableAttributedString()
        if let glass = NSImage(systemSymbolName: "magnifyingglass", accessibilityDescription: nil) {
            let a = NSTextAttachment()
            a.image = glass.withSymbolConfiguration(.init(pointSize: 11, weight: .regular))
            text.append(NSAttributedString(attachment: a))
            text.append(NSAttributedString(string: "  "))
        }
        text.append(NSAttributedString(string: name.isEmpty ? "Search files" : name))
        text.addAttributes([.foregroundColor: dim, .font: NSFont.systemFont(ofSize: 12)], range: NSRange(location: 0, length: text.length))
        searchButton.attributedTitle = text
    }
}

/// Receives the page's "pxi" messages and hands them to the right window.
final class PxiMessageRouter: NSObject, WKScriptMessageHandler {
    static let shared = PxiMessageRouter()

    func userContentController(_ controller: WKUserContentController, didReceive message: WKScriptMessage) {
        guard let d = message.body as? [String: Any],
              let app = NSApp.delegate as? AppDelegate,
              let wc = app.controller(for: message.webView) else { return }
        switch d["type"] as? String {
        case "theme":
            wc.titleBar.applyTheme(d, to: wc.window)
        case "notify":
            Notifier.shared.notify(title: d["title"] as? String ?? "pxi", body: d["body"] as? String ?? "",
                                   session: d["session"] as? String ?? "", where: d["where"] as? String ?? "", from: wc)
        case "openFolder":
            wc.chooseFolder()
        case "waiting":
            Notifier.shared.setWaiting(d["count"] as? Int ?? 0, for: wc)
        default:
            break
        }
    }
}

extension NSColor {
    /// "#rgb", "#rrggbb", "rgb(r, g, b)" or "rgba(r, g, b, a)".
    convenience init?(css: String) {
        let s = css.trimmingCharacters(in: .whitespaces).lowercased()
        if s.hasPrefix("#") {
            var hex = String(s.dropFirst())
            if hex.count == 3 { hex = hex.map { "\($0)\($0)" }.joined() }
            guard hex.count == 6, let v = UInt32(hex, radix: 16) else { return nil }
            self.init(srgbRed: CGFloat((v >> 16) & 0xff) / 255, green: CGFloat((v >> 8) & 0xff) / 255,
                      blue: CGFloat(v & 0xff) / 255, alpha: 1)
            return
        }
        guard s.hasPrefix("rgb"), let open = s.firstIndex(of: "("), let close = s.lastIndex(of: ")") else { return nil }
        let parts = s[s.index(after: open)..<close].split(separator: ",").map { Double($0.trimmingCharacters(in: .whitespaces)) ?? 0 }
        guard parts.count >= 3 else { return nil }
        self.init(srgbRed: parts[0] / 255, green: parts[1] / 255, blue: parts[2] / 255, alpha: parts.count > 3 ? parts[3] : 1)
    }
}
