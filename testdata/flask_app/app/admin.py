from flask import Blueprint

# Never registered: the scanner should still report it, flagged.
admin = Blueprint("admin", __name__, url_prefix="/admin")


@admin.route("/stats")
def stats():
    return {}
