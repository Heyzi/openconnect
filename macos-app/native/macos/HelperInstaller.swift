import Foundation
import ServiceManagement

let service = SMAppService.daemon(plistName: "org.openconnect.desktop.helper.plist")

do {
    switch CommandLine.arguments.dropFirst().first ?? "register" {
    case "register":
        if service.status == .enabled {
            try service.unregister()
        }
        try service.register()
        if service.status == .requiresApproval {
            fputs("Approve OpenConnect Desktop in System Settings > General > Login Items.\n", stderr)
            exit(77)
        }
    case "unregister":
        try service.unregister()
    default:
        fputs("usage: openconnect-helper-installer register|unregister\n", stderr)
        exit(64)
    }
} catch {
    fputs("\(error.localizedDescription)\n", stderr)
    exit(1)
}
