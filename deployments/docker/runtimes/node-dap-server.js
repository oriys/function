/**
 * Node.js DAP Server - A simple DAP-to-CDP bridge
 *
 * This script acts as a DAP server that translates between:
 * - DAP (Debug Adapter Protocol) - used by our backend
 * - CDP (Chrome DevTools Protocol) - used by Node.js --inspect
 */

const net = require('net');
const { spawn } = require('child_process');
const WebSocket = require('ws');

class NodeDAPServer {
  constructor(port) {
    this.port = port;
    this.seq = 1;
    this.pendingRequests = new Map();
    this.ws = null;
    this.debugProcess = null;
    this.inspectorUrl = null;
    this.initialized = false;
    this.launched = false;
    this.breakpoints = new Map();
    this.scriptIds = new Map(); // path -> scriptId
    this.scriptPaths = new Map(); // scriptId -> path
  }

  start() {
    const server = net.createServer((socket) => {
      console.log('[DAP] Client connected');
      this.socket = socket;
      this.buffer = '';

      socket.on('data', (data) => {
        this.buffer += data.toString();
        this.processBuffer();
      });

      socket.on('close', () => {
        console.log('[DAP] Client disconnected');
        this.cleanup();
      });

      socket.on('error', (err) => {
        console.error('[DAP] Socket error:', err.message);
      });
    });

    server.listen(this.port, '0.0.0.0', () => {
      console.log(`[DAP] Server listening on port ${this.port}`);
    });
  }

  processBuffer() {
    while (true) {
      const headerEnd = this.buffer.indexOf('\r\n\r\n');
      if (headerEnd === -1) break;

      const header = this.buffer.substring(0, headerEnd);
      const match = header.match(/Content-Length:\s*(\d+)/i);
      if (!match) break;

      const contentLength = parseInt(match[1], 10);
      const bodyStart = headerEnd + 4;
      const bodyEnd = bodyStart + contentLength;

      if (this.buffer.length < bodyEnd) break;

      const body = this.buffer.substring(bodyStart, bodyEnd);
      this.buffer = this.buffer.substring(bodyEnd);

      try {
        const message = JSON.parse(body);
        this.handleDAPMessage(message);
      } catch (e) {
        console.error('[DAP] Parse error:', e.message);
      }
    }
  }

  sendDAP(message) {
    const body = JSON.stringify(message);
    const header = `Content-Length: ${Buffer.byteLength(body)}\r\n\r\n`;
    this.socket.write(header + body);
    console.log('[DAP] Sent:', message.type, message.command || message.event || '');
  }

  sendEvent(event, body = {}) {
    this.sendDAP({
      seq: this.seq++,
      type: 'event',
      event,
      body
    });
  }

  sendResponse(request, success = true, body = {}, message = '') {
    this.sendDAP({
      seq: this.seq++,
      type: 'response',
      request_seq: request.seq,
      command: request.command,
      success,
      body,
      message: success ? undefined : message
    });
  }

  async handleDAPMessage(message) {
    console.log('[DAP] Received:', message.type, message.command || '');

    if (message.type !== 'request') return;

    const { command, arguments: args } = message;

    try {
      switch (command) {
        case 'initialize':
          this.handleInitialize(message, args);
          break;
        case 'launch':
          await this.handleLaunch(message, args);
          break;
        case 'setBreakpoints':
          await this.handleSetBreakpoints(message, args);
          break;
        case 'configurationDone':
          await this.handleConfigurationDone(message);
          break;
        case 'threads':
          this.handleThreads(message);
          break;
        case 'stackTrace':
          await this.handleStackTrace(message, args);
          break;
        case 'scopes':
          await this.handleScopes(message, args);
          break;
        case 'variables':
          await this.handleVariables(message, args);
          break;
        case 'continue':
          await this.handleContinue(message, args);
          break;
        case 'next':
          await this.handleNext(message, args);
          break;
        case 'stepIn':
          await this.handleStepIn(message, args);
          break;
        case 'stepOut':
          await this.handleStepOut(message, args);
          break;
        case 'evaluate':
          await this.handleEvaluate(message, args);
          break;
        case 'disconnect':
          this.handleDisconnect(message);
          break;
        default:
          this.sendResponse(message, false, {}, `Unknown command: ${command}`);
      }
    } catch (err) {
      console.error(`[DAP] Error handling ${command}:`, err.message);
      this.sendResponse(message, false, {}, err.message);
    }
  }

  handleInitialize(request, args) {
    this.sendResponse(request, true, {
      supportsConfigurationDoneRequest: true,
      supportsConditionalBreakpoints: true,
      supportsHitConditionalBreakpoints: true,
      supportsEvaluateForHovers: true,
      supportsSetVariable: true,
      supportsStepInTargetsRequest: false,
      supportsLogPoints: true,
      supportTerminateDebuggee: true,
      supportsTerminateRequest: true,
      exceptionBreakpointFilters: [
        { filter: 'all', label: 'All Exceptions', default: false },
        { filter: 'uncaught', label: 'Uncaught Exceptions', default: true }
      ]
    });
    this.sendEvent('initialized');
    this.initialized = true;
  }

  async handleLaunch(request, args) {
    const program = args.program || '/tmp/debug_runner.js';
    const stopOnEntry = args.stopOnEntry !== false;
    const cwd = args.cwd || '/tmp';
    const env = { ...process.env, ...args.env };

    // Start Node.js with inspector
    const inspectFlag = stopOnEntry ? '--inspect-brk=0.0.0.0:9230' : '--inspect=0.0.0.0:9230';

    console.log(`[DAP] Launching: node ${inspectFlag} ${program}`);

    this.debugProcess = spawn('node', [inspectFlag, program], {
      cwd,
      env,
      stdio: ['pipe', 'pipe', 'pipe']
    });

    this.debugProcess.stdout.on('data', (data) => {
      this.sendEvent('output', { category: 'stdout', output: data.toString() });
    });

    this.debugProcess.stderr.on('data', (data) => {
      const output = data.toString();
      // Parse inspector URL from stderr
      const match = output.match(/ws:\/\/[^\s]+/);
      if (match && !this.ws) {
        this.inspectorUrl = match[0];
        console.log('[DAP] Inspector URL:', this.inspectorUrl);
        this.connectToInspector();
      }
      // Don't forward debugger messages
      if (!output.includes('Debugger listening') && !output.includes('For help')) {
        this.sendEvent('output', { category: 'stderr', output });
      }
    });

    this.debugProcess.on('exit', (code) => {
      console.log('[DAP] Process exited with code:', code);
      this.sendEvent('exited', { exitCode: code || 0 });
      this.sendEvent('terminated');
    });

    // Wait for inspector to be ready
    await new Promise((resolve) => {
      const checkInterval = setInterval(() => {
        if (this.ws && this.ws.readyState === WebSocket.OPEN) {
          clearInterval(checkInterval);
          resolve();
        }
      }, 100);
      setTimeout(() => {
        clearInterval(checkInterval);
        resolve();
      }, 5000);
    });

    this.launched = true;
    this.sendResponse(request, true);
    this.sendEvent('process', {
      name: program,
      systemProcessId: this.debugProcess.pid,
      isLocalProcess: true,
      startMethod: 'launch'
    });
  }

  connectToInspector() {
    console.log('[DAP] Connecting to inspector...');
    this.ws = new WebSocket(this.inspectorUrl);

    this.ws.on('open', () => {
      console.log('[DAP] Connected to inspector');
      // Enable necessary domains
      this.sendCDP('Runtime.enable');
      this.sendCDP('Debugger.enable');
      this.sendCDP('Debugger.setAsyncCallStackDepth', { maxDepth: 32 });
    });

    this.ws.on('message', (data) => {
      try {
        const message = JSON.parse(data.toString());
        this.handleCDPMessage(message);
      } catch (e) {
        console.error('[CDP] Parse error:', e.message);
      }
    });

    this.ws.on('close', () => {
      console.log('[DAP] Inspector connection closed');
    });

    this.ws.on('error', (err) => {
      console.error('[DAP] Inspector error:', err.message);
    });
  }

  sendCDP(method, params = {}) {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
      return Promise.reject(new Error('Inspector not connected'));
    }

    const id = this.seq++;
    const message = { id, method, params };

    return new Promise((resolve, reject) => {
      this.pendingRequests.set(id, { resolve, reject });
      this.ws.send(JSON.stringify(message));
      console.log('[CDP] Sent:', method);

      setTimeout(() => {
        if (this.pendingRequests.has(id)) {
          this.pendingRequests.delete(id);
          reject(new Error('CDP request timeout'));
        }
      }, 10000);
    });
  }

  handleCDPMessage(message) {
    // Handle response
    if (message.id !== undefined) {
      const pending = this.pendingRequests.get(message.id);
      if (pending) {
        this.pendingRequests.delete(message.id);
        if (message.error) {
          pending.reject(new Error(message.error.message));
        } else {
          pending.resolve(message.result);
        }
      }
      return;
    }

    // Handle events
    const { method, params } = message;
    console.log('[CDP] Event:', method);

    switch (method) {
      case 'Debugger.paused':
        this.handleDebuggerPaused(params);
        break;
      case 'Debugger.resumed':
        this.sendEvent('continued', { threadId: 1, allThreadsContinued: true });
        break;
      case 'Debugger.scriptParsed':
        this.handleScriptParsed(params);
        break;
      case 'Runtime.consoleAPICalled':
        this.handleConsoleAPI(params);
        break;
      case 'Runtime.exceptionThrown':
        this.handleException(params);
        break;
    }
  }

  handleDebuggerPaused(params) {
    const { callFrames, reason, hitBreakpoints } = params;

    let stopReason = 'pause';
    if (reason === 'exception') stopReason = 'exception';
    else if (reason === 'Break on start') stopReason = 'entry';
    else if (hitBreakpoints && hitBreakpoints.length > 0) stopReason = 'breakpoint';
    else if (reason === 'step') stopReason = 'step';

    this.currentCallFrames = callFrames;

    this.sendEvent('stopped', {
      reason: stopReason,
      threadId: 1,
      allThreadsStopped: true
    });
  }

  handleScriptParsed(params) {
    const { scriptId, url } = params;
    if (url && url.startsWith('file://')) {
      const path = url.replace('file://', '');
      this.scriptIds.set(path, scriptId);
      this.scriptPaths.set(scriptId, path);
    } else if (url && !url.startsWith('node:')) {
      this.scriptIds.set(url, scriptId);
      this.scriptPaths.set(scriptId, url);
    }
  }

  handleConsoleAPI(params) {
    const { type, args } = params;
    const output = args.map(a => a.value || a.description || '').join(' ') + '\n';
    this.sendEvent('output', {
      category: type === 'error' ? 'stderr' : 'console',
      output
    });
  }

  handleException(params) {
    const { exceptionDetails } = params;
    const text = exceptionDetails.text || 'Exception';
    this.sendEvent('output', {
      category: 'stderr',
      output: `${text}\n`
    });
  }

  async handleSetBreakpoints(request, args) {
    const { source, breakpoints: bps } = args;
    const path = source.path;

    // Clear existing breakpoints for this file
    const existingBps = this.breakpoints.get(path) || [];
    for (const bp of existingBps) {
      try {
        await this.sendCDP('Debugger.removeBreakpoint', { breakpointId: bp.id });
      } catch (e) {}
    }

    const scriptId = this.scriptIds.get(path);
    const results = [];

    for (const bp of (bps || [])) {
      try {
        let result;
        if (scriptId) {
          result = await this.sendCDP('Debugger.setBreakpoint', {
            location: { scriptId, lineNumber: bp.line - 1, columnNumber: 0 },
            condition: bp.condition
          });
        } else {
          result = await this.sendCDP('Debugger.setBreakpointByUrl', {
            lineNumber: bp.line - 1,
            urlRegex: path.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'),
            condition: bp.condition
          });
        }

        results.push({
          id: results.length + 1,
          verified: true,
          line: bp.line,
          source: { path }
        });
      } catch (e) {
        results.push({
          id: results.length + 1,
          verified: false,
          line: bp.line,
          message: e.message
        });
      }
    }

    this.breakpoints.set(path, results);
    this.sendResponse(request, true, { breakpoints: results });
  }

  async handleConfigurationDone(request) {
    this.sendResponse(request, true);
    // Resume if stopped at entry
    if (this.launched) {
      // Don't auto-resume, let client control
    }
  }

  handleThreads(request) {
    this.sendResponse(request, true, {
      threads: [{ id: 1, name: 'Main Thread' }]
    });
  }

  async handleStackTrace(request, args) {
    const frames = (this.currentCallFrames || []).map((frame, index) => {
      const path = this.scriptPaths.get(frame.location.scriptId) || frame.url;
      return {
        id: index,
        name: frame.functionName || '(anonymous)',
        source: { path, name: path.split('/').pop() },
        line: frame.location.lineNumber + 1,
        column: frame.location.columnNumber + 1
      };
    });

    this.sendResponse(request, true, {
      stackFrames: frames,
      totalFrames: frames.length
    });
  }

  async handleScopes(request, args) {
    const frameId = args.frameId;
    const frame = this.currentCallFrames?.[frameId];

    if (!frame) {
      this.sendResponse(request, true, { scopes: [] });
      return;
    }

    const scopes = frame.scopeChain.map((scope, index) => ({
      name: scope.type.charAt(0).toUpperCase() + scope.type.slice(1),
      variablesReference: parseInt(scope.object.objectId?.split('.')[1] || index + 1000),
      expensive: scope.type === 'global'
    }));

    // Store scope objects for variable lookup
    this.scopeObjects = frame.scopeChain.map(s => s.object);

    this.sendResponse(request, true, { scopes });
  }

  async handleVariables(request, args) {
    const { variablesReference } = args;
    const scopeObject = this.scopeObjects?.[variablesReference - 1000];

    if (!scopeObject?.objectId) {
      this.sendResponse(request, true, { variables: [] });
      return;
    }

    try {
      const result = await this.sendCDP('Runtime.getProperties', {
        objectId: scopeObject.objectId,
        ownProperties: true
      });

      const variables = (result.result || [])
        .filter(p => !p.name.startsWith('__'))
        .map(prop => ({
          name: prop.name,
          value: prop.value?.description || prop.value?.value?.toString() || 'undefined',
          type: prop.value?.type || 'undefined',
          variablesReference: prop.value?.objectId ? parseInt(prop.value.objectId.split('.')[1]) : 0
        }));

      this.sendResponse(request, true, { variables });
    } catch (e) {
      this.sendResponse(request, true, { variables: [] });
    }
  }

  async handleContinue(request, args) {
    await this.sendCDP('Debugger.resume');
    this.sendResponse(request, true, { allThreadsContinued: true });
  }

  async handleNext(request, args) {
    await this.sendCDP('Debugger.stepOver');
    this.sendResponse(request, true);
  }

  async handleStepIn(request, args) {
    await this.sendCDP('Debugger.stepInto');
    this.sendResponse(request, true);
  }

  async handleStepOut(request, args) {
    await this.sendCDP('Debugger.stepOut');
    this.sendResponse(request, true);
  }

  async handleEvaluate(request, args) {
    const { expression, frameId } = args;

    try {
      let result;
      if (frameId !== undefined && this.currentCallFrames?.[frameId]) {
        const callFrameId = this.currentCallFrames[frameId].callFrameId;
        result = await this.sendCDP('Debugger.evaluateOnCallFrame', {
          callFrameId,
          expression,
          returnByValue: true
        });
      } else {
        result = await this.sendCDP('Runtime.evaluate', {
          expression,
          returnByValue: true
        });
      }

      const value = result.result;
      this.sendResponse(request, true, {
        result: value.description || value.value?.toString() || 'undefined',
        type: value.type,
        variablesReference: value.objectId ? parseInt(value.objectId.split('.')[1]) : 0
      });
    } catch (e) {
      this.sendResponse(request, false, {}, e.message);
    }
  }

  handleDisconnect(request) {
    this.sendResponse(request, true);
    this.cleanup();
  }

  cleanup() {
    if (this.debugProcess) {
      this.debugProcess.kill();
      this.debugProcess = null;
    }
    if (this.ws) {
      this.ws.close();
      this.ws = null;
    }
  }
}

// Start server
const port = parseInt(process.env.DAP_PORT || '9229', 10);
const server = new NodeDAPServer(port);
server.start();
