import Cocoa
import UserNotifications

/// Mac notifications and the Dock badge for Claude sessions: the page
/// (web/src/pxi-claude.js) reports when a session needs the user or finishes,
/// and how many are waiting. Clicking a notification brings that window
/// forward with the session open.
final class Notifier: NSObject, UNUserNotificationCenterDelegate {
    static let shared = Notifier()
    private var asked = false
    private var waiting: [ObjectIdentifier: Int] = [:] // per window

    private override init() {
        super.init()
        UNUserNotificationCenter.current().delegate = self
    }

    func notify(title: String, body: String, session: String, where: String, from wc: BrowserWindowController) {
        let center = UNUserNotificationCenter.current()
        let post = {
            let c = UNMutableNotificationContent()
            c.title = title
            c.body = body
            c.sound = .default
            c.threadIdentifier = session
            c.userInfo = ["session": session, "where": `where`, "window": "\(ObjectIdentifier(wc).hashValue)"]
            center.add(UNNotificationRequest(identifier: "pxi-\(session)-\(Date().timeIntervalSince1970)", content: c, trigger: nil))
        }
        if asked { post(); return }
        center.requestAuthorization(options: [.alert, .sound, .badge]) { ok, _ in
            self.asked = true
            if ok { post() }
        }
    }

    /// Sessions waiting for the user in one window; the Dock shows the total.
    func setWaiting(_ n: Int, for wc: BrowserWindowController) {
        waiting[ObjectIdentifier(wc)] = n
        refreshBadge()
    }

    func windowClosed(_ wc: BrowserWindowController) {
        waiting[ObjectIdentifier(wc)] = nil
        refreshBadge()
    }

    private func refreshBadge() {
        let total = waiting.values.reduce(0, +)
        NSApp.dockTile.badgeLabel = total > 0 ? String(total) : nil
    }

    // Shown even while pxi is in front: the page only asks when the session
    // is not the one on screen.
    func userNotificationCenter(_ center: UNUserNotificationCenter, willPresent notification: UNNotification,
                                withCompletionHandler done: @escaping (UNNotificationPresentationOptions) -> Void) {
        done([.banner, .sound])
    }

    func userNotificationCenter(_ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse,
                                withCompletionHandler done: @escaping () -> Void) {
        let info = response.notification.request.content.userInfo
        let session = info["session"] as? String ?? ""
        let place = info["where"] as? String ?? ""
        let win = info["window"] as? String ?? ""
        DispatchQueue.main.async {
            guard let app = NSApp.delegate as? AppDelegate,
                  let wc = app.allControllers.first(where: { "\(ObjectIdentifier($0).hashValue)" == win }) ?? app.allControllers.first
            else { done(); return }
            NSApp.activate(ignoringOtherApps: true)
            wc.window?.makeKeyAndOrderFront(nil)
            let js = "window.pxiNative && window.pxiNative.openClaude(\(Self.jsString(session)), \(Self.jsString(place)))"
            wc.webView.evaluateJavaScript(js)
            done()
        }
    }

    static func jsString(_ s: String) -> String {
        let data = try? JSONSerialization.data(withJSONObject: [s])
        let arr = data.flatMap { String(data: $0, encoding: .utf8) } ?? "[\"\"]"
        return String(arr.dropFirst().dropLast())
    }
}
