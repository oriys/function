#!/usr/bin/env python3
"""
Function runtime for Python 3.11
Reads function code and payload from stdin, executes, outputs result to stdout.
"""
import sys
import json
import traceback

def main():
    try:
        # Read input from stdin
        input_data = json.loads(sys.stdin.read())

        handler_path = input_data.get('handler', 'handler')
        code = input_data.get('code', '')
        payload = input_data.get('payload', {})
        env_vars = input_data.get('env', {})

        # Set environment variables
        import os
        for key, value in env_vars.items():
            os.environ[key] = value

        # Parse handler (module.function format)
        if '.' in handler_path:
            module_name, func_name = handler_path.rsplit('.', 1)
        else:
            module_name, func_name = 'handler', handler_path

        # Create a namespace and execute the code
        namespace = {}
        exec(code, namespace)

        # Get the handler function
        if func_name not in namespace:
            raise ValueError(f"Handler function '{func_name}' not found in code")

        handler = namespace[func_name]

        # Create a simple context object
        class Context:
            def __init__(self):
                self.function_name = os.environ.get('FUNCTION_NAME', 'unknown')
                self.memory_limit_in_mb = int(os.environ.get('FUNCTION_MEMORY_MB', '256'))
                self.function_version = os.environ.get('FUNCTION_VERSION', '$LATEST')
                self.aws_request_id = 'uuid-placeholder'
                self.log_group_name = '/aws/lambda/' + self.function_name
                self.log_stream_name = 'date/[$LATEST]uuid'

        # Execute the handler
        result = handler(payload, Context())

        # Output result
        print(json.dumps(result))

    except Exception as e:
        error_response = {
            "error": str(e),
            "traceback": traceback.format_exc()
        }
        print(json.dumps(error_response), file=sys.stderr)
        sys.exit(1)

if __name__ == '__main__':
    main()
