import Cocoa

/// The menu bar. Key equivalents are chosen so they never take a shortcut the page
/// uses (Cmd+W closes a file tab, Cmd+P opens files, Cmd+Shift+R, Cmd+D, ...):
/// the page sees each key first, and only keys it ignores reach the menu.
enum MainMenu {
    static func build() -> NSMenu {
        let main = NSMenu()
        main.addItem(submenu(appMenu()))
        main.addItem(submenu(fileMenu()))
        main.addItem(submenu(editMenu()))
        main.addItem(submenu(viewMenu()))
        let window = windowMenu()
        main.addItem(submenu(window))
        NSApp.windowsMenu = window
        let help = helpMenu()
        main.addItem(submenu(help))
        NSApp.helpMenu = help
        return main
    }

    private static func submenu(_ menu: NSMenu) -> NSMenuItem {
        let item = NSMenuItem(title: menu.title, action: nil, keyEquivalent: "")
        item.submenu = menu
        return item
    }

    private static func item(_ title: String, _ action: Selector?, _ key: String = "",
                             _ mods: NSEvent.ModifierFlags = .command, target: AnyObject? = nil) -> NSMenuItem {
        let i = NSMenuItem(title: title, action: action, keyEquivalent: key)
        i.keyEquivalentModifierMask = mods
        i.target = target
        return i
    }

    private static func appMenu() -> NSMenu {
        let m = NSMenu(title: "pxi")
        let d = NSApp.delegate as AnyObject?
        m.addItem(item("About pxi", #selector(AppDelegate.showAbout(_:)), target: d))
        m.addItem(.separator())
        let services = NSMenu(title: "Services")
        let servicesItem = NSMenuItem(title: "Services", action: nil, keyEquivalent: "")
        servicesItem.submenu = services
        NSApp.servicesMenu = services
        m.addItem(servicesItem)
        m.addItem(.separator())
        m.addItem(item("Hide pxi", #selector(NSApplication.hide(_:)), "h"))
        m.addItem(item("Hide Others", #selector(NSApplication.hideOtherApplications(_:)), "h", [.command, .option]))
        m.addItem(item("Show All", #selector(NSApplication.unhideAllApplications(_:))))
        m.addItem(.separator())
        m.addItem(item("Restart pxi", #selector(AppDelegate.restartApp(_:)), "r", [.command, .option, .shift], target: d))
        m.addItem(item("Quit pxi", #selector(NSApplication.terminate(_:)), "q"))
        return m
    }

    private static func fileMenu() -> NSMenu {
        let m = NSMenu(title: "File")
        let d = NSApp.delegate as AnyObject?
        m.addItem(item("New Window", #selector(AppDelegate.newDocumentWindow(_:)), "n", [.command, .shift], target: d))
        m.addItem(item("New Tab", #selector(AppDelegate.newTab(_:)), "t", target: d))
        m.addItem(item("Open Folder…", #selector(AppDelegate.openFolder(_:)), "o", target: d))
        m.addItem(.separator())
        m.addItem(item("Close Window", #selector(NSWindow.performClose(_:)), "w", [.command, .shift]))
        return m
    }

    private static func editMenu() -> NSMenu {
        let m = NSMenu(title: "Edit")
        m.addItem(item("Undo", Selector(("undo:")), "z"))
        m.addItem(item("Redo", Selector(("redo:")), "z", [.command, .shift]))
        m.addItem(.separator())
        m.addItem(item("Cut", #selector(NSText.cut(_:)), "x"))
        m.addItem(item("Copy", #selector(NSText.copy(_:)), "c"))
        m.addItem(item("Paste", #selector(NSText.paste(_:)), "v"))
        m.addItem(item("Paste and Match Style", #selector(NSTextView.pasteAsPlainText(_:)), "v", [.command, .option, .shift]))
        m.addItem(item("Select All", #selector(NSText.selectAll(_:)), "a"))
        return m
    }

    private static func viewMenu() -> NSMenu {
        let m = NSMenu(title: "View")
        m.addItem(item("Workbench", #selector(BrowserWindowController.showWorkbench(_:)), "h", [.command, .shift]))
        m.addItem(item("Back", #selector(BrowserWindowController.goBack(_:)), "["))
        m.addItem(item("Forward", #selector(BrowserWindowController.goForward(_:)), "]"))
        m.addItem(item("Reload Window", #selector(BrowserWindowController.reloadPage(_:)), "r", [.command, .option]))
        m.addItem(.separator())
        m.addItem(item("Actual Size", #selector(BrowserWindowController.actualSize(_:)), "0"))
        m.addItem(item("Zoom In", #selector(BrowserWindowController.zoomIn(_:)), "="))
        m.addItem(item("Zoom Out", #selector(BrowserWindowController.zoomOut(_:)), "-"))
        m.addItem(.separator())
        m.addItem(item("Enter Full Screen", #selector(NSWindow.toggleFullScreen(_:)), "f", [.command, .control]))
        return m
    }

    private static func windowMenu() -> NSMenu {
        let m = NSMenu(title: "Window")
        m.addItem(item("Minimize", #selector(NSWindow.performMiniaturize(_:)), "m"))
        m.addItem(item("Zoom", #selector(NSWindow.performZoom(_:))))
        m.addItem(.separator())
        m.addItem(item("Bring All to Front", #selector(NSApplication.arrangeInFront(_:))))
        return m
    }

    private static func helpMenu() -> NSMenu {
        let m = NSMenu(title: "Help")
        let d = NSApp.delegate as AnyObject?
        m.addItem(item("Show Log", #selector(AppDelegate.showLogFile(_:)), target: d))
        return m
    }
}
