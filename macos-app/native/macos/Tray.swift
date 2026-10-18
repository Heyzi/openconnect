import AppKit

final class TrayDelegate: NSObject, NSApplicationDelegate {
    private var statusItem: NSStatusItem!
    private let portalURL: URL
    private let agentPID: Int32
    private let statusPath: String
    private let buildCommit: String
    private var timer: Timer?
    private var detailItem: NSMenuItem!
    private var disconnectItem: NSMenuItem!
    private var copyServerItem: NSMenuItem!
    private var sessionCookie: String?
    private var csrfToken: String?
    private var currentServer: String?
    private lazy var origin: URL = {
        var components = URLComponents(url: portalURL, resolvingAgainstBaseURL: false)!
        components.path = ""
        components.query = nil
        return components.url!
    }()

    init(portalURL: URL, agentPID: Int32, statusPath: String, buildCommit: String) {
        self.portalURL = portalURL
        self.agentPID = agentPID
        self.statusPath = statusPath
        self.buildCommit = buildCommit
    }

    func applicationDidFinishLaunching(_ notification: Notification) {
        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.squareLength)
        if let button = statusItem.button {
            button.image = statusIcon(color: .labelColor)
            button.imagePosition = .imageOnly
            button.setAccessibilityLabel("OpenConnect Desktop")
        }
        let menu = NSMenu()
        let title = NSMenuItem(title: "OpenConnect Desktop", action: nil, keyEquivalent: "")
        title.isEnabled = false
        menu.addItem(title)
        let version = Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "unknown"
        let versionItem = NSMenuItem(title: "Version \(version) · build \(buildCommit)", action: nil, keyEquivalent: "")
        versionItem.isEnabled = false
        menu.addItem(versionItem)
        menu.addItem(.separator())
        detailItem = NSMenuItem(title: "Disconnected", action: nil, keyEquivalent: "")
        detailItem.isEnabled = false
        menu.addItem(detailItem)
        disconnectItem = NSMenuItem(title: "Disconnect", action: #selector(disconnect), keyEquivalent: "")
        disconnectItem.target = self
        disconnectItem.isHidden = true
        menu.addItem(disconnectItem)
        copyServerItem = NSMenuItem(title: "Copy Server Address", action: #selector(copyServerAddress), keyEquivalent: "")
        copyServerItem.target = self
        copyServerItem.isHidden = true
        menu.addItem(copyServerItem)
        menu.addItem(withTitle: "Open Portal", action: #selector(openPortal), keyEquivalent: "o").target = self
        menu.addItem(.separator())
        menu.addItem(withTitle: "Quit", action: #selector(quit), keyEquivalent: "q").target = self
        statusItem.menu = menu
        NSWorkspace.shared.notificationCenter.addObserver(self, selector: #selector(didWake), name: NSWorkspace.didWakeNotification, object: nil)
        updateStatus()
        timer = Timer.scheduledTimer(timeInterval: 1, target: self, selector: #selector(updateStatus), userInfo: nil, repeats: true)
    }

    @objc private func updateStatus() {
        if kill(agentPID, 0) != 0 {
            NSApplication.shared.terminate(nil)
            return
        }
        guard let data = FileManager.default.contents(atPath: statusPath),
              let object = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let state = object["state"] as? String else { return }
        let color: NSColor
        switch state { case "connected": color = .systemGreen; case "connecting", "disconnecting": color = .systemOrange; case "error": color = .systemRed; default: color = .labelColor }
        statusItem.button?.image = statusIcon(color: color)
        statusItem.button?.toolTip = "OpenConnect Desktop — \(state)"
        let active = ["connected", "connecting"].contains(state)
        disconnectItem.isHidden = !active
        copyServerItem.isHidden = !active
        currentServer = object["server"] as? String
        let profileName = object["profileName"] as? String
        switch state {
        case "error":
            detailItem.title = (object["lastError"] as? String).map { "Error: \($0)" } ?? "Error"
        case "connected", "connecting", "disconnecting":
            detailItem.title = [profileName, state.capitalized].compactMap { $0 }.joined(separator: " — ")
        default:
            detailItem.title = "Disconnected"
        }
    }

    private func ensureSession(_ completion: @escaping (Bool) -> Void) {
        if sessionCookie != nil, csrfToken != nil { completion(true); return }
        // portalURL is the one-time /bootstrap?token=... link, which redirects to
        // "/" after setting the session cookie. URLSession follows that redirect
        // itself, so `response` here is the reply to "/" and never carries the
        // Set-Cookie header — only HTTPCookieStorage, which URLSession populates
        // from every response in the chain, still has it.
        URLSession.shared.dataTask(with: portalURL) { [weak self] _, _, _ in
            guard let self,
                  let cookie = HTTPCookieStorage.shared.cookies(for: self.origin)?.first(where: { $0.name == "oc_session" }) else {
                DispatchQueue.main.async { completion(false) }
                return
            }
            self.sessionCookie = "\(cookie.name)=\(cookie.value)"
            var request = URLRequest(url: self.origin.appendingPathComponent("api/v1/session"))
            request.setValue(self.sessionCookie, forHTTPHeaderField: "Cookie")
            URLSession.shared.dataTask(with: request) { data, _, _ in
                guard let data,
                      let object = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
                      let csrf = object["csrfToken"] as? String else {
                    DispatchQueue.main.async { completion(false) }
                    return
                }
                self.csrfToken = csrf
                DispatchQueue.main.async { completion(true) }
            }.resume()
        }.resume()
    }

    @objc private func disconnect() {
        ensureSession { [weak self] ok in
            guard ok, let self else { return }
            var request = URLRequest(url: self.origin.appendingPathComponent("api/v1/disconnect"))
            request.httpMethod = "POST"
            request.setValue(self.sessionCookie, forHTTPHeaderField: "Cookie")
            request.setValue(self.csrfToken, forHTTPHeaderField: "X-CSRF-Token")
            URLSession.shared.dataTask(with: request).resume()
        }
    }

    private func statusIcon(color: NSColor) -> NSImage {
        let image = NSImage(size: NSSize(width: 18, height: 18), flipped: false) { _ in
            let shield = NSBezierPath()
            shield.move(to: NSPoint(x: 9, y: 16))
            shield.curve(to: NSPoint(x: 3.5, y: 13.8), controlPoint1: NSPoint(x: 7.2, y: 15.3), controlPoint2: NSPoint(x: 5.4, y: 14.6))
            shield.line(to: NSPoint(x: 3.5, y: 9))
            shield.curve(to: NSPoint(x: 9, y: 2.2), controlPoint1: NSPoint(x: 3.5, y: 5.8), controlPoint2: NSPoint(x: 5.6, y: 3.3))
            shield.curve(to: NSPoint(x: 14.5, y: 9), controlPoint1: NSPoint(x: 12.4, y: 3.3), controlPoint2: NSPoint(x: 14.5, y: 5.8))
            shield.line(to: NSPoint(x: 14.5, y: 13.8))
            shield.curve(to: NSPoint(x: 9, y: 16), controlPoint1: NSPoint(x: 12.6, y: 14.6), controlPoint2: NSPoint(x: 10.8, y: 15.3))
            shield.close()
            color.withAlphaComponent(0.16).setFill()
            shield.fill()
            color.setStroke()
            shield.lineWidth = 1.45
            shield.lineJoinStyle = .round
            shield.stroke()

            color.setFill()
            NSBezierPath(ovalIn: NSRect(x: 7, y: 7, width: 4, height: 4)).fill()
            return true
        }
        image.isTemplate = false
        return image
    }

    @objc private func openPortal() { NSWorkspace.shared.open(portalURL) }
    @objc private func copyServerAddress() {
        guard let server = currentServer else { return }
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(server, forType: .string)
    }
    @objc private func didWake() { kill(agentPID, SIGUSR1) }
    @objc private func quit() {
        kill(agentPID, SIGTERM)
        NSApplication.shared.terminate(nil)
    }
}

guard CommandLine.arguments.count == 5,
      let url = URL(string: CommandLine.arguments[1]),
      let pid = Int32(CommandLine.arguments[2]) else { exit(64) }
let app = NSApplication.shared
app.setActivationPolicy(.accessory)
let delegate = TrayDelegate(portalURL: url, agentPID: pid, statusPath: CommandLine.arguments[3], buildCommit: CommandLine.arguments[4])
app.delegate = delegate
app.run()
