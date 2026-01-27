#!/usr/bin/env python3
"""
GDB-based DAP Server for Rust Debugging

This script implements a Debug Adapter Protocol (DAP) server that translates
DAP requests to GDB Machine Interface (MI) commands. It allows IDE-style
debugging of Rust programs through a standard DAP interface.
"""

import json
import os
import re
import socket
import subprocess
import sys
import threading
import time
from typing import Optional, Dict, Any, List

class GDBDAPServer:
    def __init__(self, port: int, program: str):
        self.port = port
        self.program = program
        self.gdb_process: Optional[subprocess.Popen] = None
        self.client_socket: Optional[socket.socket] = None
        self.seq = 0
        self.running = True
        self.initialized = False
        self.breakpoints: Dict[str, List[int]] = {}  # file -> [lines]
        self.stopped_thread = 1
        self.gdb_lock = threading.Lock()

    def start(self):
        """Start the DAP server"""
        server = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        server.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        server.bind(('0.0.0.0', self.port))
        server.listen(1)

        print(f"GDB DAP Server listening on port {self.port}", file=sys.stderr)

        while self.running:
            try:
                self.client_socket, addr = server.accept()
                print(f"Client connected from {addr}", file=sys.stderr)
                self.handle_client()
            except Exception as e:
                print(f"Server error: {e}", file=sys.stderr)
                break

    def handle_client(self):
        """Handle a single DAP client connection"""
        buffer = ""
        try:
            while self.running:
                data = self.client_socket.recv(4096)
                if not data:
                    break

                buffer += data.decode('utf-8')

                # Parse DAP messages (Content-Length header format)
                while True:
                    match = re.match(r'Content-Length: (\d+)\r\n\r\n', buffer)
                    if not match:
                        break

                    content_length = int(match.group(1))
                    header_length = match.end()

                    if len(buffer) < header_length + content_length:
                        break  # Wait for more data

                    message = buffer[header_length:header_length + content_length]
                    buffer = buffer[header_length + content_length:]

                    self.handle_dap_message(json.loads(message))

        except Exception as e:
            print(f"Client handler error: {e}", file=sys.stderr)
        finally:
            if self.gdb_process:
                self.gdb_process.terminate()
            if self.client_socket:
                self.client_socket.close()

    def send_response(self, request: Dict, body: Optional[Dict] = None, success: bool = True, message: str = ""):
        """Send a DAP response"""
        self.seq += 1
        response = {
            "seq": self.seq,
            "type": "response",
            "request_seq": request["seq"],
            "command": request["command"],
            "success": success
        }
        if body:
            response["body"] = body
        if message:
            response["message"] = message
        self.send_message(response)

    def send_event(self, event: str, body: Optional[Dict] = None):
        """Send a DAP event"""
        self.seq += 1
        msg = {
            "seq": self.seq,
            "type": "event",
            "event": event
        }
        if body:
            msg["body"] = body
        self.send_message(msg)

    def send_message(self, msg: Dict):
        """Send a DAP message to the client"""
        content = json.dumps(msg)
        header = f"Content-Length: {len(content)}\r\n\r\n"
        try:
            self.client_socket.sendall((header + content).encode('utf-8'))
        except Exception as e:
            print(f"Send error: {e}", file=sys.stderr)

    def handle_dap_message(self, message: Dict):
        """Handle incoming DAP message"""
        msg_type = message.get("type")

        if msg_type == "request":
            command = message.get("command")
            args = message.get("arguments", {})

            handler = getattr(self, f"handle_{command}", None)
            if handler:
                handler(message, args)
            else:
                print(f"Unhandled command: {command}", file=sys.stderr)
                self.send_response(message, success=False, message=f"Unknown command: {command}")

    def handle_initialize(self, request: Dict, args: Dict):
        """Handle initialize request"""
        self.send_response(request, {
            "supportsConfigurationDoneRequest": True,
            "supportsSetVariable": True,
            "supportsConditionalBreakpoints": True,
            "supportsHitConditionalBreakpoints": True,
            "supportsEvaluateForHovers": True,
            "supportsFunctionBreakpoints": True,
            "supportsStepBack": False,
            "supportsRestartFrame": False,
            "supportTerminateDebuggee": True,
            "supportsTerminateRequest": True,
        })
        self.send_event("initialized")

    def handle_launch(self, request: Dict, args: Dict):
        """Handle launch request"""
        program = args.get("program", self.program)
        cwd = args.get("cwd", "/tmp/project")
        stop_on_entry = args.get("stopOnEntry", True)

        # Start GDB with MI interface
        try:
            self.gdb_process = subprocess.Popen(
                ["gdb", "--interpreter=mi", program],
                stdin=subprocess.PIPE,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                cwd=cwd,
                text=True,
                bufsize=1
            )

            # Read initial GDB output
            self._read_gdb_until_prompt()

            # Set breakpoint on main if stopOnEntry
            if stop_on_entry:
                self._send_gdb_command("-break-insert main")
                self._read_gdb_until_prompt()

            # Run the program
            self._send_gdb_command("-exec-run")
            output = self._read_gdb_until_prompt()

            self.send_response(request)
            self.send_event("process", {
                "name": program,
                "systemProcessId": self.gdb_process.pid,
                "isLocalProcess": True,
                "startMethod": "launch"
            })

            # Check if we hit the breakpoint
            if "*stopped" in output:
                self.send_event("stopped", {
                    "reason": "entry" if stop_on_entry else "breakpoint",
                    "threadId": 1,
                    "allThreadsStopped": True
                })

        except Exception as e:
            self.send_response(request, success=False, message=str(e))

    def handle_setBreakpoints(self, request: Dict, args: Dict):
        """Handle setBreakpoints request"""
        source = args.get("source", {})
        path = source.get("path", "")
        breakpoints = args.get("breakpoints", [])

        result_breakpoints = []

        # Clear existing breakpoints for this file
        self._send_gdb_command("-break-delete")
        self._read_gdb_until_prompt()

        # Set new breakpoints
        for i, bp in enumerate(breakpoints):
            line = bp.get("line")
            condition = bp.get("condition", "")

            cmd = f"-break-insert {path}:{line}"
            if condition:
                cmd += f" -c \"{condition}\""

            self._send_gdb_command(cmd)
            output = self._read_gdb_until_prompt()

            verified = "done" in output.lower()
            result_breakpoints.append({
                "id": i + 1,
                "verified": verified,
                "line": line,
                "source": {"path": path}
            })

        self.send_response(request, {"breakpoints": result_breakpoints})

    def handle_configurationDone(self, request: Dict, args: Dict):
        """Handle configurationDone request"""
        self.send_response(request)

    def handle_threads(self, request: Dict, args: Dict):
        """Handle threads request"""
        self.send_response(request, {
            "threads": [
                {"id": 1, "name": "main"}
            ]
        })

    def handle_stackTrace(self, request: Dict, args: Dict):
        """Handle stackTrace request"""
        self._send_gdb_command("-stack-list-frames")
        output = self._read_gdb_until_prompt()

        frames = []
        # Parse GDB MI output for frames
        frame_pattern = r'frame=\{level="(\d+)",addr="([^"]*)",func="([^"]*)",file="([^"]*)"(?:,fullname="([^"]*)")?(?:,line="(\d+)")?\}'
        for match in re.finditer(frame_pattern, output):
            level, addr, func, file, fullname, line = match.groups()
            frames.append({
                "id": int(level),
                "name": func,
                "source": {"path": fullname or file, "name": file},
                "line": int(line) if line else 0,
                "column": 0
            })

        self.send_response(request, {
            "stackFrames": frames,
            "totalFrames": len(frames)
        })

    def handle_scopes(self, request: Dict, args: Dict):
        """Handle scopes request"""
        frame_id = args.get("frameId", 0)
        self.send_response(request, {
            "scopes": [
                {"name": "Locals", "variablesReference": 1, "expensive": False},
                {"name": "Arguments", "variablesReference": 2, "expensive": False},
            ]
        })

    def handle_variables(self, request: Dict, args: Dict):
        """Handle variables request"""
        ref = args.get("variablesReference", 0)

        if ref == 1:
            # Locals
            self._send_gdb_command("-stack-list-locals --all-values")
        else:
            # Arguments
            self._send_gdb_command("-stack-list-arguments --all-values")

        output = self._read_gdb_until_prompt()

        variables = []
        # Parse GDB MI output for variables
        var_pattern = r'\{name="([^"]*)",(?:type="([^"]*)",)?value="([^"]*)"\}'
        for match in re.finditer(var_pattern, output):
            name, var_type, value = match.groups()
            variables.append({
                "name": name,
                "value": value,
                "type": var_type or "unknown",
                "variablesReference": 0
            })

        self.send_response(request, {"variables": variables})

    def handle_continue(self, request: Dict, args: Dict):
        """Handle continue request"""
        self._send_gdb_command("-exec-continue")
        threading.Thread(target=self._wait_for_stop).start()
        self.send_response(request, {"allThreadsContinued": True})

    def handle_next(self, request: Dict, args: Dict):
        """Handle next (step over) request"""
        self._send_gdb_command("-exec-next")
        threading.Thread(target=self._wait_for_stop).start()
        self.send_response(request)

    def handle_stepIn(self, request: Dict, args: Dict):
        """Handle step in request"""
        self._send_gdb_command("-exec-step")
        threading.Thread(target=self._wait_for_stop).start()
        self.send_response(request)

    def handle_stepOut(self, request: Dict, args: Dict):
        """Handle step out request"""
        self._send_gdb_command("-exec-finish")
        threading.Thread(target=self._wait_for_stop).start()
        self.send_response(request)

    def handle_evaluate(self, request: Dict, args: Dict):
        """Handle evaluate request"""
        expression = args.get("expression", "")
        self._send_gdb_command(f'-data-evaluate-expression "{expression}"')
        output = self._read_gdb_until_prompt()

        # Parse value from output
        value_match = re.search(r'value="([^"]*)"', output)
        value = value_match.group(1) if value_match else "undefined"

        self.send_response(request, {
            "result": value,
            "variablesReference": 0
        })

    def handle_disconnect(self, request: Dict, args: Dict):
        """Handle disconnect request"""
        if self.gdb_process:
            self._send_gdb_command("-gdb-exit")
            self.gdb_process.terminate()
        self.running = False
        self.send_response(request)

    def handle_terminate(self, request: Dict, args: Dict):
        """Handle terminate request"""
        if self.gdb_process:
            self._send_gdb_command("-exec-abort")
            self._send_gdb_command("-gdb-exit")
            self.gdb_process.terminate()
        self.send_response(request)

    def _send_gdb_command(self, command: str):
        """Send a command to GDB"""
        with self.gdb_lock:
            if self.gdb_process and self.gdb_process.stdin:
                self.gdb_process.stdin.write(command + "\n")
                self.gdb_process.stdin.flush()

    def _read_gdb_until_prompt(self, timeout: float = 5.0) -> str:
        """Read GDB output until we get the prompt"""
        output = []
        start = time.time()

        while time.time() - start < timeout:
            if self.gdb_process and self.gdb_process.stdout:
                try:
                    line = self.gdb_process.stdout.readline()
                    if line:
                        output.append(line)
                        # GDB MI prompt is (gdb)
                        if "(gdb)" in line or "^done" in line or "^error" in line:
                            break
                except:
                    break
            time.sleep(0.01)

        return "".join(output)

    def _wait_for_stop(self):
        """Wait for the debuggee to stop"""
        while self.running and self.gdb_process:
            line = self.gdb_process.stdout.readline()
            if "*stopped" in line:
                reason = "breakpoint"
                if "reason=\"end-stepping-range\"" in line:
                    reason = "step"
                elif "reason=\"exited" in line:
                    reason = "exited"
                    self.send_event("terminated")
                    return

                self.send_event("stopped", {
                    "reason": reason,
                    "threadId": 1,
                    "allThreadsStopped": True
                })
                break
            elif "^running" in line:
                continue
            elif "*running" in line:
                continue

def main():
    port = int(os.environ.get("DAP_PORT", "4711"))
    program = os.environ.get("PROGRAM", "/tmp/project/target/debug/debug_runner")

    server = GDBDAPServer(port, program)
    server.start()

if __name__ == "__main__":
    main()
