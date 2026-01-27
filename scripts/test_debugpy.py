#!/usr/bin/env python3
"""
Test script for debugpy DAP communication.
Run this script first, then connect from the frontend.

Usage:
  python3 test_debugpy.py

This will start debugpy listening on port 5678.
"""

import debugpy
import time

# Configure debugpy to listen on port 5678
debugpy.listen(("127.0.0.1", 5678))
print("debugpy listening on 127.0.0.1:5678")
print("Waiting for debugger to attach...")

# Wait for the debugger to attach
debugpy.wait_for_client()
print("Debugger attached!")

# Sample code to debug
def factorial(n):
    """Calculate factorial of n"""
    if n <= 1:
        return 1
    return n * factorial(n - 1)

def main():
    # Set a breakpoint here
    print("Starting main function")

    x = 10
    y = 20
    z = x + y

    print(f"x = {x}, y = {y}, z = {z}")

    result = factorial(5)
    print(f"factorial(5) = {result}")

    items = ["apple", "banana", "cherry"]
    for i, item in enumerate(items):
        print(f"{i}: {item}")

    print("Done!")

if __name__ == "__main__":
    main()
