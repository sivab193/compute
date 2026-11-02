"""A trivial pool job: prints host info and writes one artifact."""
import platform
import sys

print("hello from the shared compute pool")
print("python:", sys.version.split()[0])
print("machine:", platform.machine(), platform.system())

with open("result.txt", "w") as f:
    f.write("computed answer = 42\n")

print("wrote result.txt")
