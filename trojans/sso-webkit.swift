#!/usr/bin/swift
import AppKit
import WebKit

func errlog(_ s: String) { try? FileHandle.standardError.write(contentsOf: Data((s + "\n").utf8)) }
func die(_ s: String) -> Never { errlog(s); exit(1) }
func truncated(_ s: String, _ n: Int = 64) -> String { s.count > n ? String(s.prefix(n)) + "..." : s }

let gateway: String = {
    let raw = CommandLine.arguments.count > 2
        ? CommandLine.arguments[2].trimmingCharacters(in: .whitespacesAndNewlines) : ""
    if raw.isEmpty { die("ERROR: gateway hostname missing (expected argv[2])") }
    return raw.lowercased()
}()

// The gateway emits the token only for this UA; a browser UA gets a "download the app" page.
let gpUserAgent = "PAN GlobalProtect"
let preloginURLString = "https://\(gateway)/ssl-vpn/prelogin.esp?tmp=tmp&clientVer=4100&clientos=Mac"

let requestTimeout = 20.0
let resourceTimeout = 30.0
let loginTimeout = 240.0
let captureTimeout = 120.0
let maxRedirects = 10
let maxBodyBytes = 256 * 1024

func rx(_ p: String) -> NSRegularExpression {
    try! NSRegularExpression(pattern: p, options: [.caseInsensitive, .dotMatchesLineSeparators])
}

let actionRE = rx("<form[^>]*\\saction=\"([^\"]+)\"")
let samlResponseRE = rx("\\sname=\"SAMLResponse\"[^>]*\\svalue=\"([^\"]+)\"")
let relayStateRE = rx("\\sname=\"RelayState\"[^>]*\\svalue=\"([^\"]*)\"")
let samlRequestInputRE = rx("\\sname=\"SAMLRequest\"[^>]*\\svalue=\"([^\"]+)\"")
let samlRequestRE = rx("<saml-request>(.*?)</saml-request>")

func firstMatch(_ re: NSRegularExpression, in text: String) -> String? {
    let range = NSRange(text.startIndex..., in: text)
    guard let m = re.firstMatch(in: text, range: range), m.numberOfRanges > 1,
          let r = Range(m.range(at: 1), in: text) else { return nil }
    return String(text[r])
}

func formEncode(_ s: String) -> String {
    let unreserved = CharacterSet(charactersIn:
        "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~")
    return (s.addingPercentEncoding(withAllowedCharacters: unreserved) ?? s)
        .replacingOccurrences(of: "%20", with: "+")
}

let xmlEntities: [String: Character] = ["amp": "&", "lt": "<", "gt": ">", "quot": "\"", "apos": "'"]

func scalarChar(_ code: UInt32) -> Character? {
    guard code >= 0x20, code != 0x7F, !(0x80...0x9F).contains(code),
          let u = Unicode.Scalar(code) else { return nil }
    return Character(u)
}

func htmlUnescape(_ s: String) -> String {
    guard s.contains("&") else { return s }
    let chars = Array(s)
    var out = ""
    out.reserveCapacity(chars.count)
    var i = 0
    while i < chars.count {
        if chars[i] == "&",
           let semi = chars[(i + 1)..<min(i + 13, chars.count)].firstIndex(of: ";") {
            let body = String(chars[(i + 1)..<semi])
            let code: UInt32? = body.hasPrefix("#x") || body.hasPrefix("#X")
                ? UInt32(body.dropFirst(2), radix: 16)
                : body.hasPrefix("#") ? UInt32(body.dropFirst(1), radix: 10) : nil
            if let d = code.flatMap(scalarChar) ?? xmlEntities[body] {
                out.append(d); i = semi + 1; continue
            }
        }
        out.append(chars[i]); i += 1
    }
    return out
}

enum SamlStart {
    case redirect(URL)
    case post(action: URL, body: Data, html: String)
}

func fetchSamlStart(using session: URLSession) -> SamlStart? {
    guard let pre = URL(string: preloginURLString) else {
        errlog("prelogin failed: bad URL for gateway '\(gateway)'"); return nil
    }
    var req = URLRequest(url: pre); req.httpMethod = "POST"
    req.setValue(gpUserAgent, forHTTPHeaderField: "User-Agent")
    let sem = DispatchSemaphore(value: 0)
    var result: SamlStart?
    var failure: String?
    session.dataTask(with: req) { data, resp, err in
        defer { sem.signal() }
        if let err = err { failure = err.localizedDescription; return }
        let status = (resp as? HTTPURLResponse).map { " (HTTP \($0.statusCode))" } ?? ""
        guard let data = data else { failure = "empty prelogin response" + status; return }
        guard let b64 = firstMatch(samlRequestRE, in: String(decoding: data.prefix(maxBodyBytes), as: UTF8.self)),
              let dd = Data(base64Encoded: b64, options: .ignoreUnknownCharacters),
              let payload = String(data: dd, encoding: .utf8) else {
            failure = "no usable <saml-request> in prelogin response" + status; return
        }
        let trimmed = payload.trimmingCharacters(in: .whitespacesAndNewlines)
        if trimmed.lowercased().hasPrefix("http") {
            guard let u = URL(string: trimmed), u.scheme == "https" else {
                failure = "saml-request is not https: \(truncated(trimmed))"; return
            }
            result = .redirect(u)
        } else {
            guard let action = firstMatch(actionRE, in: payload).map(htmlUnescape),
                  let actionURL = URL(string: action), actionURL.scheme == "https",
                  let samlReq = firstMatch(samlRequestInputRE, in: payload).map(htmlUnescape) else {
                failure = "could not parse POST-binding saml-request form" + status; return
            }
            var body = "SAMLRequest=\(formEncode(samlReq))"
            if let relay = firstMatch(relayStateRE, in: payload).map(htmlUnescape) {
                body += "&RelayState=\(formEncode(relay))"
            }
            result = .post(action: actionURL, body: Data(body.utf8), html: payload)
        }
    }.resume()
    sem.wait()
    if result == nil, let f = failure { errlog("prelogin failed: \(f)") }
    return result
}

func emitTokenAndExit(cookie: String, user: String) -> Never {
    let out = "passwd=\(cookie)\nuser=\(user)\nusergroup=gateway:prelogin-cookie\n"
    try? FileHandle.standardOutput.write(contentsOf: Data(out.utf8))
    exit(0)
}

final class Capturer: NSObject, URLSessionTaskDelegate {
    var preloginCookie: String?
    var samlUsername: String?
    var redirectCount = 0
    let cookies: [HTTPCookie]
    lazy var session: URLSession = {
        let cfg = URLSessionConfiguration.ephemeral
        cfg.httpCookieAcceptPolicy = .always
        cfg.timeoutIntervalForRequest = requestTimeout
        cfg.timeoutIntervalForResource = resourceTimeout
        for c in self.cookies { cfg.httpCookieStorage?.setCookie(c) }
        return URLSession(configuration: cfg, delegate: self, delegateQueue: nil)
    }()
    init(cookies: [HTTPCookie]) { self.cookies = cookies }

    func scan(_ resp: HTTPURLResponse) {
        if let c = resp.value(forHTTPHeaderField: "prelogin-cookie"), !c.isEmpty { preloginCookie = c }
        if let u = resp.value(forHTTPHeaderField: "saml-username"), !u.isEmpty { samlUsername = u }
    }

    var captured: Bool { preloginCookie != nil && samlUsername != nil }

    // The token can ride on a 302, so inspect redirect responses too.
    func urlSession(_ session: URLSession, task: URLSessionTask,
                    willPerformHTTPRedirection response: HTTPURLResponse,
                    newRequest request: URLRequest,
                    completionHandler: @escaping (URLRequest?) -> Void) {
        scan(response)
        if captured { completionHandler(nil); return }
        redirectCount += 1
        guard redirectCount <= maxRedirects, let u = request.url, u.scheme == "https" else {
            errlog("  refusing redirect to \(request.url.map { truncated($0.absoluteString) } ?? "?")")
            completionHandler(nil); return
        }
        var req = request
        req.setValue(gpUserAgent, forHTTPHeaderField: "User-Agent")
        completionHandler(req)
    }

    func send(_ url: URL, method: String = "GET", body: Data? = nil, contentType: String? = nil)
        -> (Data, HTTPURLResponse)? {
        redirectCount = 0
        var req = URLRequest(url: url)
        req.httpMethod = method
        req.setValue(gpUserAgent, forHTTPHeaderField: "User-Agent")
        if let ct = contentType { req.setValue(ct, forHTTPHeaderField: "Content-Type") }
        req.httpBody = body
        let sem = DispatchSemaphore(value: 0)
        var out: (Data, HTTPURLResponse)?
        var failure: String?
        session.dataTask(with: req) { data, resp, err in
            if let err = err { failure = err.localizedDescription }
            if let d = data, let r = resp as? HTTPURLResponse { out = (d, r) }
            sem.signal()
        }.resume()
        sem.wait()
        if out == nil { errlog("  \(method) \(url.host ?? "?") failed: \(failure ?? "no response")") }
        return out
    }

    func run() -> Bool {
        guard let start = fetchSamlStart(using: session) else { return false }
        let idp: (Data, HTTPURLResponse)?
        switch start {
        case .redirect(let url):
            idp = send(url)
        case .post(let action, let body, _):
            idp = send(action, method: "POST", body: body, contentType: "application/x-www-form-urlencoded")
        }
        guard let (idpBody, idpResp) = idp else { return false }
        if captured { return true }
        let html = String(decoding: idpBody.prefix(maxBodyBytes), as: UTF8.self)
        guard let action = firstMatch(actionRE, in: html),
              let samlResp = firstMatch(samlResponseRE, in: html),
              let actionURL = URL(string: htmlUnescape(action)),
              actionURL.scheme == "https", actionURL.host?.lowercased() == gateway else {
            errlog("could not parse SAML form from IdP response (HTTP \(idpResp.statusCode); SSO cookie not carried?)")
            let snippet = html
                .replacingOccurrences(of: "value=\"[^\"]*\"", with: "value=\"...\"", options: .regularExpression)
                .replacingOccurrences(of: "\n", with: " ")
            errlog("  IdP returned: \(truncated(snippet, 200))")
            return false
        }
        var bodyStr = "SAMLResponse=\(formEncode(htmlUnescape(samlResp)))"
        if let relay = firstMatch(relayStateRE, in: html) {
            bodyStr += "&RelayState=\(formEncode(htmlUnescape(relay)))"
        }
        guard let (_, acsResp) = send(actionURL, method: "POST", body: Data(bodyStr.utf8),
                                      contentType: "application/x-www-form-urlencoded") else {
            return false
        }
        scan(acsResp)
        if !captured {
            let missing = [("prelogin-cookie", preloginCookie), ("saml-username", samlUsername)]
                .filter { $0.1 == nil }.map { $0.0 }.joined(separator: ", ")
            errlog("ACS response missing \(missing) (HTTP \(acsResp.statusCode))")
        }
        return captured
    }
}

final class App: NSObject, NSApplicationDelegate, WKNavigationDelegate {
    var window: NSWindow!
    var webView: WKWebView!
    var done = false
    var leftGateway = false

    func applicationDidFinishLaunching(_ note: Notification) {
        webView = WKWebView(frame: .zero, configuration: WKWebViewConfiguration())
        webView.navigationDelegate = self
        window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 520, height: 680),
                          styleMask: [.titled, .closable],
                          backing: .buffered, defer: false)
        window.contentView = webView
        window.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)

        DispatchQueue.global().async {
            let cfg = URLSessionConfiguration.ephemeral
            cfg.timeoutIntervalForRequest = requestTimeout
            cfg.timeoutIntervalForResource = resourceTimeout
            let session = URLSession(configuration: cfg)
            defer { session.finishTasksAndInvalidate() }
            guard let start = fetchSamlStart(using: session) else {
                DispatchQueue.main.async { die("ERROR: could not reach \(gateway) prelogin") }
                return
            }
            DispatchQueue.main.async {
                switch start {
                case .redirect(let url):
                    self.webView.load(URLRequest(url: url))
                case .post(_, _, let html):
                    self.webView.loadHTMLString(html, baseURL: URL(string: "https://\(gateway)/"))
                }
            }
        }

        DispatchQueue.main.asyncAfter(deadline: .now() + loginTimeout) {
            if !self.done { die("ERROR: timed out waiting for login (\(Int(loginTimeout))s)") }
        }
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ app: NSApplication) -> Bool {
        if !done { die("ERROR: login window closed before completing") }
        return false
    }

    func webView(_ webView: WKWebView,
                 didReceiveServerRedirectForProvisionalNavigation navigation: WKNavigation!) {
        leftGateway = true
    }
    func webView(_ webView: WKWebView, didFailProvisionalNavigation navigation: WKNavigation!,
                 withError error: Error) { failNavigation(error) }
    func webView(_ webView: WKWebView, didFail navigation: WKNavigation!,
                 withError error: Error) { failNavigation(error) }
    func failNavigation(_ error: Error) {
        if done { return }
        // Ignore a superseded redirect and an MFA app-scheme handoff (e.g. duo://); let the timeout govern.
        if let e = error as? URLError, e.code == .cancelled || e.code == .unsupportedURL { return }
        errlog("note: login navigation issue: \(error.localizedDescription)")
    }

    func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
        guard !done, let host = webView.url?.host?.lowercased() else { return }
        if host != gateway { leftGateway = true; return }
        guard leftGateway else { return }
        done = true
        errlog("Login detected, capturing token...")

        DispatchQueue.main.asyncAfter(deadline: .now() + captureTimeout) {
            die("ERROR: token capture timed out (\(Int(captureTimeout))s)")
        }

        webView.configuration.websiteDataStore.httpCookieStore.getAllCookies { cookies in
            DispatchQueue.global().async {
                let cap = Capturer(cookies: cookies)
                _ = cap.run()
                cap.session.finishTasksAndInvalidate()
                guard let pc = cap.preloginCookie, let user = cap.samlUsername else {
                    die("ERROR: token capture failed (see messages above)")
                }
                DispatchQueue.main.async {
                    emitTokenAndExit(cookie: pc, user: user)
                }
            }
        }
    }
}

signal(SIGPIPE, SIG_IGN)

guard gateway.range(of: "^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$",
                    options: .regularExpression) != nil,
      URL(string: preloginURLString) != nil else {
    die("ERROR: invalid gateway hostname: '\(gateway)'")
}

let app = NSApplication.shared
let delegate = App()
app.delegate = delegate
app.setActivationPolicy(.regular)
app.run()
