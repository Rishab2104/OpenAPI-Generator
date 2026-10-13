from . import bp


@bp.route("/", methods=["GET", "POST"])
def users():
    return []


@bp.route("/<int:user_id>", methods=("GET", "PUT", "DELETE"))
def user(user_id):
    return {}


@bp.get("/<uuid:token>/verify")
def verify(token):
    return {}


@bp.route("/files/<path:filename>")
def files(filename):
    return ""


# @bp.route("/commented-out")
def not_a_route():
    text = '@bp.route("/in-a-string")'
    return text
