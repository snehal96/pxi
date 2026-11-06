import Foundation

/// Runs the bundled pxi server as a workbench and finds its URL.
///
/// pxi allows one workbench per user, recorded in <state>/workbench.lock. If one
/// is already serving (the `pxi` CLI, or a pxi app that crashed), pxi attaches to it
/// and leaves it running on quit. Otherwise pxi starts its own on a fixed port,
/// so the page's origin, and with it the browser storage the page keeps (layout,
/// open tabs), stays the same from launch to launch.
final class PxServer {
    static let preferredPort = 47770

    enum StartResult {
        case success(URL)
        case failure(String)
    }

    private(set) var process: Process?
    private(set) var owned = false
    let logURL: URL

    private let binary: URL

    init() {
        binary = Bundle.main.resourceURL!.appendingPathComponent("pxi")
        let logs = FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent("Library/Logs/pxi")
        try? FileManager.default.createDirectory(at: logs, withIntermediateDirectories: true)
        logURL = logs.appendingPathComponent("pxi.log")
    }

    /// Blocking; call off the main thread.
    func start(env: [String: String]) -> StartResult {
        let lock = Self.lockURL(env)
        if let url = Self.runningWorkbench(lock: lock) {
            owned = false
            return .success(url)
        }

        FileManager.default.createFile(atPath: logURL.path, contents: nil)
        guard let log = try? FileHandle(forWritingTo: logURL) else {
            return .failure("Cannot write the log at \(logURL.path).")
        }
        let p = Process()
        p.executableURL = binary
        p.arguments = ["-no-open", "-no-update", "-no-color", "-no-telemetry",
                       "-host", "127.0.0.1", "-port", String(Self.preferredPort)]
        p.environment = env
        p.currentDirectoryURL = FileManager.default.homeDirectoryForCurrentUser
        p.standardOutput = log
        p.standardError = log
        p.standardInput = FileHandle.nullDevice
        do {
            try p.run()
        } catch {
            return .failure("Cannot start the pxi server: \(error.localizedDescription)")
        }
        process = p

        let deadline = Date().addingTimeInterval(20)
        while Date() < deadline {
            if let url = Self.runningWorkbench(lock: lock) {
                // Ours unless the server exited because another workbench won the race.
                owned = p.isRunning
                return .success(url)
            }
            if !p.isRunning {
                return .failure("The pxi server exited with status \(p.terminationStatus).\n\n\(tail())")
            }
            Thread.sleep(forTimeInterval: 0.05)
        }
        return .failure("The pxi server did not start within 20 seconds.\n\n\(tail())")
    }

    /// Stops the workbench if the app started it. The server handles SIGTERM by stopping
    /// every workspace and removing PR checkouts, so it gets a moment for that.
    func stop() {
        guard owned, let p = process, p.isRunning else { return }
        p.terminate()
        let deadline = Date().addingTimeInterval(3)
        while p.isRunning && Date() < deadline {
            Thread.sleep(forTimeInterval: 0.05)
        }
        if p.isRunning {
            kill(p.processIdentifier, SIGKILL)
        }
    }

    func tail(lines: Int = 15) -> String {
        guard let text = try? String(contentsOf: logURL, encoding: .utf8) else { return "" }
        return text.split(separator: "\n", omittingEmptySubsequences: false).suffix(lines).joined(separator: "\n")
    }

    // MARK: - the workbench lock

    static func lockURL(_ env: [String: String]) -> URL {
        if let xdg = env["XDG_STATE_HOME"], !xdg.isEmpty {
            return URL(fileURLWithPath: xdg).appendingPathComponent("pxi/workbench.lock")
        }
        return FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent(".pxi/workbench.lock")
    }

    /// The URL in the lock file, if a workbench there answers its ping.
    static func runningWorkbench(lock: URL) -> URL? {
        guard let data = try? Data(contentsOf: lock),
              let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let s = obj["url"] as? String, let url = URL(string: s) else { return nil }
        let ping = url.appendingPathComponent("api/wb/ping")
        var req = URLRequest(url: ping, cachePolicy: .reloadIgnoringLocalCacheData, timeoutInterval: 0.5)
        req.httpMethod = "GET"
        let done = DispatchSemaphore(value: 0)
        var ok = false
        URLSession.shared.dataTask(with: req) { data, _, _ in
            if let data, let j = try? JSONSerialization.jsonObject(with: data) as? [String: Any] {
                ok = (j["workbench"] as? Bool) == true
            }
            done.signal()
        }.resume()
        _ = done.wait(timeout: .now() + 1)
        return ok ? url : nil
    }
}
