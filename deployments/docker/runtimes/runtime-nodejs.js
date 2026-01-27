#!/usr/bin/env node
/**
 * Function runtime for Node.js 20
 * Reads function code and payload from stdin, executes, outputs result to stdout.
 */

const vm = require('vm');

async function main() {
    let input = '';

    // Read all input from stdin
    for await (const chunk of process.stdin) {
        input += chunk;
    }

    try {
        const data = JSON.parse(input);
        const handlerPath = data.handler || 'handler';
        const code = data.code || '';
        const payload = data.payload || {};
        const envVars = data.env || {};

        // Set environment variables
        Object.assign(process.env, envVars);

        // Parse handler
        const parts = handlerPath.split('.');
        const funcName = parts.length > 1 ? parts[parts.length - 1] : handlerPath;

        // Create sandbox with module.exports
        const sandbox = {
            module: { exports: {} },
            exports: {},
            require: require,
            console: console,
            process: process,
            Buffer: Buffer,
            setTimeout: setTimeout,
            setInterval: setInterval,
            clearTimeout: clearTimeout,
            clearInterval: clearInterval,
            Promise: Promise,
        };

        // Execute the code
        vm.runInNewContext(code, sandbox);

        // Get handler from module.exports or exports
        let handler = sandbox.module.exports[funcName] || sandbox.exports[funcName] || sandbox[funcName];

        if (typeof handler !== 'function') {
            throw new Error(`Handler function '${funcName}' not found or not a function`);
        }

        // Execute handler (support async)
        const result = await handler(payload);

        // Output result
        console.log(JSON.stringify(result));

    } catch (error) {
        console.error(JSON.stringify({
            error: error.message,
            stack: error.stack
        }));
        process.exit(1);
    }
}

main();
