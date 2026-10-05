const rl = @import("raylib");

const Rectangle = struct {
    x: f32,
    y: f32,
    width: f32,
    height: f32,

    pub fn intersects(self: Rectangle, other: Rectangle) bool {
        return self.x < other.x + other.width and
            self.x + self.width > other.x and
            self.y < other.y + other.height and
            self.y + self.height > other.y;
    }
};

const Player = struct {
    position_x: f32,
    position_y: f32,
    width: f32,
    height: f32,
    speed: f32,

    pub fn init(position_x: f32, position_y: f32, width: f32, height: f32) @This() {
        return Player{
            .position_x = position_x,
            .position_y = position_y,
            .width = width,
            .height = height,
            .speed = 5.0,
        };
    }

    pub fn update(self: *@This()) void {
        if (rl.isKeyDown(rl.KeyboardKey.right)) {
            self.position_x += self.speed;
        }
        if (rl.isKeyDown(rl.KeyboardKey.left)) {
            self.position_x -= self.speed;
        }

        if (self.position_x < 0) {
            self.position_x = 0;
        }

        if (self.position_x + self.width > @as(f32, @floatFromInt(rl.getScreenWidth()))) {
            self.position_x = @as(f32, @floatFromInt(rl.getScreenWidth())) - self.width;
        }
    }

    pub fn getRect(self: @This()) Rectangle {
        return .{
            .x = self.position_x,
            .y = self.position_y,
            .width = self.width,
            .height = self.height,
        };
    }

    pub fn draw(self: @This()) void {
        rl.drawRectangle(
            @trunc(self.position_x),
            @trunc(self.position_y),
            @trunc(self.width),
            @trunc(self.height),
            rl.Color.blue,
        );
    }
};

const Bullet = struct {
    position_x: f32,
    position_y: f32,
    width: f32,
    height: f32,
    speed: f32,
    active: bool,

    pub fn init(position_x: f32, position_y: f32, width: f32, height: f32) @This() {
        return .{
            .position_x = position_x,
            .position_y = position_y,
            .width = width,
            .height = height,
            .speed = 10.0,
            .active = false,
        };
    }

    pub fn update(self: *@This()) void {
        if (self.active) {
            self.position_y -= self.speed;
            if (self.position_y < 0) {
                self.active = false;
            }
        }
    }

    pub fn draw(self: @This()) void {
        if (self.active) {
            rl.drawRectangle(
                @intFromFloat(self.position_x),
                @intFromFloat(self.position_y),
                @intFromFloat(self.width),
                @intFromFloat(self.height),
                rl.Color.red,
            );
        }
    }

    pub fn getRect(self: @This()) Rectangle {
        return .{
            .x = self.position_x,
            .y = self.position_y,
            .width = self.width,
            .height = self.height,
        };
    }
};

const Invader = struct {
    position_x: f32,
    position_y: f32,
    width: f32,
    height: f32,
    speed: f32,
    alive: bool,

    pub fn init(position_x: f32, position_y: f32, width: f32, height: f32) @This() {
        return .{
            .position_x = position_x,
            .position_y = position_y,
            .width = width,
            .height = height,
            .speed = 5.0,
            .alive = true,
        };
    }

    pub fn update(self: *@This(), dx: f32, dy: f32) void {
        self.position_x += dx;
        self.position_y += dy;
    }

    pub fn draw(self: @This()) void {
        if (self.alive) {
            rl.drawRectangle(
                @trunc(self.position_x),
                @trunc(self.position_y),
                @trunc(self.width),
                @trunc(self.height),
                rl.Color.green,
            );
        }
    }

    pub fn getRect(self: @This()) Rectangle {
        return .{
            .x = self.position_x,
            .y = self.position_y,
            .width = self.width,
            .height = self.height,
        };
    }
};

const Enemy = struct {
    position_x: f32,
    position_y: f32,
    width: f32,
    height: f32,
    speed: f32,
    active: bool,

    pub fn init(position_x: f32, position_y: f32, width: f32, height: f32) @This() {
        return .{
            .position_x = position_x,
            .position_y = position_y,
            .width = width,
            .height = height,
            .speed = 5.0,
            .active = true,
        };
    }
    pub fn draw(self: @This()) void {
        if (self.active) {
            rl.drawRectangle(@trunc(self.position_x), @trunc(self.position_y), @trunc(self.width), @trunc(self.height), rl.Color.green);
        }
    }
};

const EnemyBullet = struct {
    position_x: f32,
    position_y: f32,
    width: f32,
    height: f32,
    speed: f32,
    active: bool,

    pub fn init(position_x: f32, position_y: f32, width: f32, height: f32) @This() {
        return .{
            .position_x = position_x,
            .position_y = position_y,
            .width = width,
            .height = height,
            .speed = 5.0,
            .active = true,
        };
    }

    pub fn getRect(self: @This()) Rectangle {
        return .{
            .x = self.position_x,
            .y = self.position_y,
            .width = self.width,
            .height = self.height,
        };
    }

    pub fn update(self: *@This(), screen_height: i32) void {
        if (self.active) {
            self.position_y += self.speed;
            if (self.position_y > @as(f32, @floatFromInt(screen_height))) {
                self.active = false;
            }
        }
    }

    pub fn draw(self: @This()) void {
        if (self.active) {
            rl.drawRectangle(
                @intFromFloat(self.position_x),
                @intFromFloat(self.position_y),
                @intFromFloat(self.width),
                @intFromFloat(self.height),
                rl.Color.yellow,
            );
        }
    }
};

pub fn main() void {
    const screenWidth = 800;
    const screenHeight = 800;
    const maxBullets = 10;
    const bulletWidth = 4.0;
    const bulletHeight = 10.0;
    const invaderRows = 5;
    const invaderCols = 11;
    const invaderWidth = 40.0;
    const invaderHeight = 30.0;
    const invaderStartX = 100.0;
    const invaderStartY = 50.0;
    const invaderSpacingX = 60.0;
    const invaderSpacingY = 40.0;
    const invaderSpeed = 5.0;
    const invaderMoveDelay = 30;
    const invaderDropDistance = 20.0;
    const maxEnemyBullets = 20;
    const enemyShootDelay = 60;
    const enemyShootChance = 5;
    var game_over: bool = false;
    var enemy_shoot_timer: i32 = 0;
    var invader_direction: f32 = 1.0;
    var move_timer: i32 = 0;
    var score: i32 = 0;

    rl.initWindow(screenWidth, screenHeight, "Zig Invaders");
    defer rl.closeWindow();

    const playerWidth = 50.0;
    const playerHeight = 30.0;

    var player = Player.init(
        @as(f32, @floatFromInt(screenWidth)) / 2 - playerWidth / 2,
        @as(f32, @floatFromInt(screenHeight)) - 60.0,
        playerWidth,
        playerHeight,
    );

    var bullets: [maxBullets]Bullet = undefined;
    for (&bullets) |*bullet| {
        bullet.* = Bullet.init(
            0,
            0,
            bulletWidth,
            bulletHeight,
        );
    }

    var enemy_bullets: [maxEnemyBullets]EnemyBullet = undefined;
    for (&enemy_bullets) |*bullet| {
        bullet.* = EnemyBullet.init(0, 0, bulletWidth, bulletHeight);
    }

    var invaders: [invaderRows][invaderCols]Invader = undefined;
    for (&invaders, 0..) |*row, i| {
        for (row, 0..) |*invader, j| {
            const x = invaderStartX + @as(f32, @floatFromInt(j)) * invaderSpacingX;
            const y = invaderStartY + @as(f32, @floatFromInt(i)) * invaderSpacingY;
            invader.* = Invader.init(
                x,
                y,
                invaderWidth,
                invaderHeight,
            );
        }
    }

    rl.setTargetFPS(60);

    while (!rl.windowShouldClose()) {

        // ============== Frame Setup Logic ==============

        rl.beginDrawing();
        defer rl.endDrawing();

        rl.clearBackground(rl.Color.black);

        // ============== Update Logic ==============
        player.update();

        if (rl.isKeyPressed(rl.KeyboardKey.space)) {
            for (&bullets) |*bullet| {
                if (!bullet.active) {
                    bullet.position_x = player.position_x + player.width / 2 - bullet.width / 2;
                    bullet.position_y = player.position_y;
                    bullet.active = true;
                    break;
                }
            }
        }

        for (&bullets) |*bullet| {
            bullet.update();
        }

        for (&bullets) |*bullet| {
            if (bullet.active) {
                for (&invaders) |*row| {
                    for (row) |*invader| {
                        if (invader.alive) {
                            if (bullet.getRect().intersects(invader.getRect())) {
                                bullet.active = false;
                                invader.alive = false;
                                score += 10;
                                break;
                            }
                        }
                    }
                }
            }
        }

        for (&enemy_bullets) |*bullet| {
            bullet.update(screenHeight);
        }
        enemy_shoot_timer += 1;

        if (enemy_shoot_timer >= enemyShootDelay) {
            enemy_shoot_timer = 0;
            for (&invaders) |*row| {
                for (row) |*invader| {
                    if (invader.alive and rl.getRandomValue(0, 100) < enemyShootChance) {
                        for (&enemy_bullets) |*bullet| {
                            if (!bullet.active) {
                                bullet.position_x = invader.position_x + invader.width / 2 - bullet.width / 2;
                                bullet.position_y = invader.position_y + invader.height;
                                bullet.active = true;
                                break;
                            }
                        }
                    }
                }
            }
        }

        move_timer += 1;
        if (move_timer >= invaderMoveDelay) {
            move_timer = 0;

            var hit_edge = false;
            for (&invaders) |*row| {
                for (row) |*invader| {
                    if (invader.alive) {
                        const next_x = invader.position_x + (invaderSpeed * invader_direction);
                        if (next_x < 0 or next_x + invader.width > @as(f32, @floatFromInt(screenWidth))) {
                            hit_edge = true;
                            break;
                        }
                    }
                    invader.update(invaderSpeed * invader_direction, 0);
                }
                if (hit_edge) break;
            }
            if (hit_edge) {
                invader_direction *= -1.0;
                for (&invaders) |*row| {
                    for (row) |*invader| {
                        invader.update(0, invaderDropDistance);
                    }
                }
            } else {
                for (&invaders) |*row| {
                    for (row) |*invader| {
                        invader.update(invaderSpeed * invader_direction, 0);
                    }
                }
            }
        }

        //============== Draw Logic ==============
        player.draw();

        for (&bullets) |*bullet| {
            bullet.draw();
        }

        for (&invaders) |*row| {
            for (row) |*invader| {
                invader.draw();
            }
        }

        for (&enemy_bullets) |*bullet| {
            bullet.draw();
        }

        const score_text = rl.textFormat("Score %d", .{score});
        rl.drawText(
            score_text,
            20,
            screenHeight - 20,
            20,
            rl.Color.white,
        );

        rl.drawText(
            "Zig Invaders - Space to shoot, ESC to quit",
            20,
            20,
            20,
            rl.Color.green,
        );
    }
}
