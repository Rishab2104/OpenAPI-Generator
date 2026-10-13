from flask import Flask

app = Flask(__name__)


@app.route("/test-only")
def only_in_tests():
    return ""
