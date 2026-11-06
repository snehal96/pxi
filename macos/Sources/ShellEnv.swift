import Foundation

/// The login shell's environment.
///
/// An app opened from the Dock or Finder gets a bare environment: no PATH from
/// ~/.zshrc and no exported tokens. px0 starts git, gh, claude, codex and
/// language servers, and reads GITHUB_TOKEN and friends, so it needs what a
/// terminal would have. A shell that hangs (a prompt in a dotfile) is given up
/// on after `timeout` seconds and a PATH with the usual install folders is used.
enum ShellEnv {
    private static let marker = "__PXI_ENV_BEGIN__"

    static func load(timeout: TimeInterval = 4) -> [String: String] {
        var env = ProcessInfo.processInfo.environment
        if let shellEnv = askShell(timeout: timeout) {
            for (k, v) in shellEnv where !skipped.contains(k) {
                env[k] = v
            }
        } else {
            env["PATH"] = fallbackPath(env["PATH"])
        }
        return env
    }

    /// Variables that describe the throwaway shell, not the user's setup.
    private static let skipped: Set<String> = ["SHLVL", "PWD", "OLDPWD", "_", "TERM", "TERM_PROGRAM", "TERM_SESSION_ID"]

    private static func askShell(timeout: TimeInterval) -> [String: String]? {
        let shell = ProcessInfo.processInfo.environment["SHELL"] ?? "/bin/zsh"
        let p = Process()
        p.executableURL = URL(fileURLWithPath: shell)
        p.arguments = ["-ilc", "printf '\\n\(marker)\\n'; /usr/bin/env -0"]
        let out = Pipe()
        p.standardOutput = out
        p.standardError = FileHandle.nullDevice
        p.standardInput = FileHandle.nullDevice

        // Read as data arrives: a shell that starts a background agent can keep
        // the pipe open after it exits, so waiting for EOF could hang.
        let lock = NSLock()
        var data = Data()
        out.fileHandleForReading.readabilityHandler = { h in
            let chunk = h.availableData
            lock.lock(); data.append(chunk); lock.unlock()
        }
        let done = DispatchSemaphore(value: 0)
        p.terminationHandler = { _ in done.signal() }
        do { try p.run() } catch { return nil }
        if done.wait(timeout: .now() + timeout) == .timedOut {
            p.terminate()
            out.fileHandleForReading.readabilityHandler = nil
            return nil
        }
        // Let the last chunk land.
        Thread.sleep(forTimeInterval: 0.05)
        out.fileHandleForReading.readabilityHandler = nil
        lock.lock(); let all = data; lock.unlock()

        guard let text = String(data: all, encoding: .utf8),
              let range = text.range(of: marker + "\n") else { return nil }
        var env: [String: String] = [:]
        for entry in text[range.upperBound...].split(separator: "\0") {
            guard let eq = entry.firstIndex(of: "=") else { continue }
            env[String(entry[..<eq])] = String(entry[entry.index(after: eq)...])
        }
        return env["PATH"] == nil ? nil : env
    }

    private static func fallbackPath(_ current: String?) -> String {
        let home = NSHomeDirectory()
        let extra = ["\(home)/.local/bin", "\(home)/go/bin", "\(home)/.cargo/bin", "/opt/homebrew/bin", "/opt/homebrew/sbin", "/usr/local/bin"]
        let base = (current ?? "/usr/bin:/bin:/usr/sbin:/sbin").split(separator: ":").map(String.init)
        var seen = Set<String>()
        return (extra + base).filter { seen.insert($0).inserted }.joined(separator: ":")
    }
}
