import Foundation

/// Talks to the local agent's portal API on behalf of the tray.
final class PortalClient {
    private let portalURL: URL
    private let origin: URL

    init(portalURL: URL) {
        self.portalURL = portalURL
        var components = URLComponents(url: portalURL, resolvingAgainstBaseURL: false)!
        components.path = ""
        components.query = nil
        origin = components.url!
    }

    /// Logs in from scratch on every call: logging out of the portal rotates the
    /// session and CSRF token, so anything cached here would silently turn into a
    /// 401. The ephemeral session keeps its own cookies, carrying the bootstrap
    /// cookie through the redirect. Completes on the main queue with an error
    /// message, or nil on success.
    func disconnect(completion: @escaping (String?) -> Void) {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.timeoutIntervalForRequest = 10
        let session = URLSession(configuration: configuration)
        let finish: (String?) -> Void = { message in
            session.finishTasksAndInvalidate()
            DispatchQueue.main.async { completion(message) }
        }
        session.dataTask(with: portalURL) { [origin] _, _, error in
            if let error { finish(error.localizedDescription); return }
            session.dataTask(with: origin.appendingPathComponent("api/v1/session")) { data, _, error in
                guard let data,
                      let object = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
                      let csrf = object["csrfToken"] as? String else {
                    finish(error?.localizedDescription ?? "The agent refused the tray session")
                    return
                }
                var request = URLRequest(url: origin.appendingPathComponent("api/v1/disconnect"))
                request.httpMethod = "POST"
                request.setValue(csrf, forHTTPHeaderField: "X-CSRF-Token")
                session.dataTask(with: request) { data, response, error in
                    if let error { finish(error.localizedDescription); return }
                    let status = (response as? HTTPURLResponse)?.statusCode ?? 0
                    if (200..<300).contains(status) { finish(nil); return }
                    let body = data.flatMap { String(data: $0, encoding: .utf8) }?.trimmingCharacters(in: .whitespacesAndNewlines)
                    finish(body?.isEmpty == false ? body : "HTTP \(status)")
                }.resume()
            }.resume()
        }.resume()
    }
}
