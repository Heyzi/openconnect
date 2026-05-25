#!/usr/bin/env python3
# Fake HIP wrapper for testing run_hip_script().
# Records invocation args to a file, then outputs minimal valid HIP XML.
import sys
import os

# record args for test verification
record_file = os.environ.get('HIP_WRAPPER_RECORD')
if record_file:
    with open(record_file, 'w') as f:
        f.write(' '.join(sys.argv) + '\n')

# output minimal HIP report XML that the server will accept
print('<?xml version="1.0" encoding="UTF-8"?>')
print('<hip-report>')
print('  <md5-sum>aabbccddeeff00112233445566778899</md5-sum>')
print('  <user-name>test</user-name>')
print('  <ip-address>127.0.0.1</ip-address>')
print('  <generate-time>01/01/2024 00:00:00</generate-time>')
print('  <hip-report-version>4</hip-report-version>')
print('  <categories/>')
print('</hip-report>')
