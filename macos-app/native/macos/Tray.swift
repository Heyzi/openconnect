import AppKit

final class TrayDelegate: NSObject, NSApplicationDelegate {
    private var statusItem: NSStatusItem!
    private let portalURL: URL
    private let agentPID: Int32
    private let statusPath: String
    private var timer: Timer?

    init(portalURL: URL, agentPID: Int32, statusPath: String) {
        self.portalURL = portalURL
        self.agentPID = agentPID
        self.statusPath = statusPath
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
        menu.addItem(.separator())
        menu.addItem(withTitle: "Open Portal", action: #selector(openPortal), keyEquivalent: "o").target = self
        menu.addItem(.separator())
        menu.addItem(withTitle: "Quit", action: #selector(quit), keyEquivalent: "q").target = self
        statusItem.menu = menu
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
    @objc private func quit() {
        kill(agentPID, SIGTERM)
        NSApplication.shared.terminate(nil)
    }
}

guard CommandLine.arguments.count == 4,
      let url = URL(string: CommandLine.arguments[1]),
      let pid = Int32(CommandLine.arguments[2]) else { exit(64) }
let app = NSApplication.shared
app.setActivationPolicy(.accessory)
let delegate = TrayDelegate(portalURL: url, agentPID: pid, statusPath: CommandLine.arguments[3])
app.delegate = delegate
app.run()
