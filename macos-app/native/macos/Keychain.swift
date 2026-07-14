import Foundation
import Security

let service = "org.openconnect.desktop.profile"
guard CommandLine.arguments.count == 3 else { exit(64) }
let operation = CommandLine.arguments[1], account = CommandLine.arguments[2]
let key: [String: Any] = [kSecClass as String: kSecClassGenericPassword, kSecAttrService as String: service, kSecAttrAccount as String: account]
func fail(_ status: OSStatus) -> Never { FileHandle.standardError.write(Data((SecCopyErrorMessageString(status, nil) as String? ?? "Keychain error").utf8)); exit(1) }
switch operation {
case "set":
    let data = FileHandle.standardInput.readDataToEndOfFile()
    let status = SecItemCopyMatching(key as CFDictionary, nil)
    if status == errSecSuccess { let update = [kSecValueData as String: data]; let result = SecItemUpdate(key as CFDictionary, update as CFDictionary); if result != errSecSuccess { fail(result) } }
    else if status == errSecItemNotFound { var item = key; item[kSecValueData as String] = data; let result = SecItemAdd(item as CFDictionary, nil); if result != errSecSuccess { fail(result) } }
    else { fail(status) }
case "get":
    var query = key; query[kSecReturnData as String] = true; query[kSecMatchLimit as String] = kSecMatchLimitOne
    var result: CFTypeRef?; let status = SecItemCopyMatching(query as CFDictionary, &result); if status != errSecSuccess { fail(status) }
    FileHandle.standardOutput.write(result as! Data)
case "delete":
    let status = SecItemDelete(key as CFDictionary); if status != errSecSuccess && status != errSecItemNotFound { fail(status) }
default: exit(64)
}
